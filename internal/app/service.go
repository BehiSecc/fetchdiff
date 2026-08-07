package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fetchdiff/fetchdiff/internal/compare"
	"github.com/fetchdiff/fetchdiff/internal/extract"
	"github.com/fetchdiff/fetchdiff/internal/fetch"
	"github.com/fetchdiff/fetchdiff/internal/model"
	"github.com/fetchdiff/fetchdiff/internal/schedule"
	"github.com/fetchdiff/fetchdiff/internal/store"
)

const FailureThreshold = 3

var targetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Fetcher interface {
	Fetch(context.Context, fetch.Request) (fetch.Response, error)
}

type Service struct {
	store   *store.Store
	fetcher Fetcher
	now     func() time.Time
}

type AddInput struct {
	Name    string
	URL     string
	Every   time.Duration
	Headers map[string]string
}

type CheckResult struct {
	Target         model.Target
	History        model.HistoryEntry
	Changed        bool
	Skipped        bool
	FailureReached bool
	PreviousHash   string
	PreviousSize   int64
	Recovered      bool
}

type RevisionDiff struct {
	Target   model.Target
	Current  model.HistoryEntry
	Previous model.HistoryEntry
	Diff     compare.Diff
}

func New(store *store.Store, fetcher Fetcher) *Service {
	return &Service{store: store, fetcher: fetcher, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Targets() ([]model.Target, error) {
	return s.store.Targets()
}

func (s *Service) Target(name string) (model.Target, error) {
	return s.store.Target(name)
}

func (s *Service) History(name string) ([]model.HistoryEntry, error) {
	target, err := s.store.Target(name)
	if err != nil {
		return nil, err
	}
	return s.store.History(target.ID)
}

func (s *Service) Add(ctx context.Context, input AddInput) (model.Target, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !targetNamePattern.MatchString(input.Name) {
		return model.Target{}, errors.New("name must start with a letter or number and contain at most 64 letters, numbers, dots, underscores, or dashes")
	}
	if input.Every <= 0 {
		return model.Target{}, errors.New("check interval must be greater than zero")
	}
	if err := validateURL(input.URL); err != nil {
		return model.Target{}, err
	}
	if _, err := s.store.Target(input.Name); err == nil {
		return model.Target{}, fmt.Errorf("target name %q already exists", input.Name)
	} else if !strings.Contains(err.Error(), "not found") {
		return model.Target{}, err
	}

	response, err := s.fetcher.Fetch(ctx, fetch.Request{URL: input.URL, Headers: input.Headers})
	if err != nil {
		return model.Target{}, fmt.Errorf("create baseline: %w", err)
	}
	if response.NotModified {
		return model.Target{}, errors.New("create baseline: server returned 304 without an existing snapshot")
	}
	now := s.now()
	hash := contentHash(response.Content)
	if err := s.store.PutSnapshot(hash, response.Content); err != nil {
		return model.Target{}, err
	}
	target := model.Target{
		ID:            newID(),
		Revision:      1,
		Name:          input.Name,
		URL:           input.URL,
		Every:         input.Every,
		Headers:       input.Headers,
		CreatedAt:     now,
		NextCheckAt:   now.Add(input.Every),
		LastCheckedAt: now,
		LastSuccessAt: now,
		LastChangedAt: now,
		SnapshotHash:  hash,
		SnapshotSize:  int64(len(response.Content)),
		ContentType:   response.ContentType,
		ResourceType:  extract.ResourceType(response.EffectiveURL, response.ContentType),
		ETag:          response.ETag,
		LastModified:  response.LastModified,
		EffectiveURL:  response.EffectiveURL,
		StatusCode:    response.StatusCode,
	}
	entry := model.HistoryEntry{
		TargetID:     target.ID,
		CheckedAt:    now,
		Outcome:      model.OutcomeBaseline,
		Hash:         hash,
		Size:         target.SnapshotSize,
		StatusCode:   target.StatusCode,
		EffectiveURL: target.EffectiveURL,
	}
	if err := s.store.CreateTarget(target, entry); err != nil {
		return model.Target{}, err
	}
	return target, nil
}

func (s *Service) CheckTarget(ctx context.Context, name string, force bool) (CheckResult, error) {
	target, err := s.store.Target(name)
	if err != nil {
		return CheckResult{}, err
	}
	now := s.now()
	if !force && !schedule.Due(target, now) {
		return CheckResult{Target: target, Skipped: true}, nil
	}
	return s.checkWithConflictRetry(ctx, target)
}

func (s *Service) CheckDue(ctx context.Context) ([]CheckResult, error) {
	targets, err := s.store.Targets()
	if err != nil {
		return nil, err
	}
	results := make([]CheckResult, 0, len(targets))
	var failures []error
	now := s.now()
	for _, target := range targets {
		if !schedule.Due(target, now) {
			continue
		}
		result, err := s.checkWithConflictRetry(ctx, target)
		results = append(results, result)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", target.Name, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return results, errors.Join(failures...)
}

func (s *Service) checkWithConflictRetry(ctx context.Context, target model.Target) (CheckResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		result, err := s.check(ctx, target)
		if !errors.Is(err, store.ErrTargetChanged) {
			return result, err
		}
		fresh, loadErr := s.store.Target(target.ID)
		if loadErr != nil {
			return CheckResult{}, errors.Join(err, loadErr)
		}
		target = fresh
	}
	return CheckResult{}, store.ErrTargetChanged
}

func (s *Service) check(ctx context.Context, target model.Target) (CheckResult, error) {
	previous := target
	response, fetchErr := s.fetcher.Fetch(ctx, fetch.Request{
		URL:          target.URL,
		Headers:      target.Headers,
		ETag:         target.ETag,
		LastModified: target.LastModified,
	})
	now := s.now()
	target.LastCheckedAt = now
	target.NextCheckAt = now.Add(target.Every)
	expectedRevision := target.Revision
	target.Revision++
	if fetchErr != nil {
		target.ConsecutiveFailures++
		target.LastError = fetchErr.Error()
		target.LastErrorFingerprint = errorFingerprint(fetchErr)
		reached := !target.FailureReported && target.ConsecutiveFailures >= FailureThreshold
		if reached {
			target.FailureReported = true
		}
		entry := model.HistoryEntry{
			TargetID:           target.ID,
			CheckedAt:          now,
			Outcome:            model.OutcomeFailure,
			Hash:               target.SnapshotHash,
			Size:               target.SnapshotSize,
			StatusCode:         response.StatusCode,
			PreviousStatusCode: target.StatusCode,
			EffectiveURL:       response.EffectiveURL,
			PreviousURL:        target.EffectiveURL,
			StatusChanged:      response.StatusCode != 0 && response.StatusCode != target.StatusCode,
			RedirectChanged:    response.EffectiveURL != "" && response.EffectiveURL != target.EffectiveURL,
			Error:              fetchErr.Error(),
		}
		if err := s.store.UpdateTarget(target, expectedRevision, entry); err != nil {
			return CheckResult{}, errors.Join(fetchErr, err)
		}
		return CheckResult{Target: target, History: entry, FailureReached: reached}, fetchErr
	}
	if response.NotModified && target.SnapshotHash == "" {
		return CheckResult{}, errors.New("server returned 304 without an existing snapshot")
	}

	wasReported := target.FailureReported
	target.ConsecutiveFailures = 0
	target.LastError = ""
	target.LastErrorFingerprint = ""
	target.FailureReported = false
	target.LastSuccessAt = now

	statusChanged := false
	redirectChanged := response.EffectiveURL != "" && response.EffectiveURL != target.EffectiveURL
	changed := false
	newHash := target.SnapshotHash
	newSize := target.SnapshotSize
	if response.NotModified {
		if response.ETag != "" {
			target.ETag = response.ETag
		}
		if response.LastModified != "" {
			target.LastModified = response.LastModified
		}
		if response.EffectiveURL != "" {
			target.EffectiveURL = response.EffectiveURL
		}
	} else {
		newHash = contentHash(response.Content)
		newSize = int64(len(response.Content))
		changed = newHash != target.SnapshotHash
		statusChanged = response.StatusCode != target.StatusCode
		if changed {
			if err := s.store.PutSnapshot(newHash, response.Content); err != nil {
				return CheckResult{}, err
			}
			target.LastChangedAt = now
		}
		target.SnapshotHash = newHash
		target.SnapshotSize = newSize
		target.ContentType = response.ContentType
		target.ResourceType = extract.ResourceType(response.EffectiveURL, response.ContentType)
		target.ETag = response.ETag
		target.LastModified = response.LastModified
		target.EffectiveURL = response.EffectiveURL
		target.StatusCode = response.StatusCode
	}

	outcome := model.OutcomeUnchanged
	if changed {
		outcome = model.OutcomeChanged
	} else if wasReported {
		outcome = model.OutcomeRecovery
	}
	entry := model.HistoryEntry{
		TargetID:           target.ID,
		CheckedAt:          now,
		Outcome:            outcome,
		Hash:               target.SnapshotHash,
		PreviousHash:       previous.SnapshotHash,
		Size:               target.SnapshotSize,
		PreviousSize:       previous.SnapshotSize,
		StatusCode:         target.StatusCode,
		PreviousStatusCode: previous.StatusCode,
		EffectiveURL:       target.EffectiveURL,
		PreviousURL:        previous.EffectiveURL,
		StatusChanged:      statusChanged,
		RedirectChanged:    redirectChanged,
		Recovered:          wasReported,
	}
	if err := s.store.UpdateTarget(target, expectedRevision, entry); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		Target:       target,
		History:      entry,
		Changed:      changed,
		PreviousHash: previous.SnapshotHash,
		PreviousSize: previous.SnapshotSize,
		Recovered:    wasReported,
	}, nil
}

func (s *Service) RevisionDiff(name string) (RevisionDiff, error) {
	target, err := s.store.Target(name)
	if err != nil {
		return RevisionDiff{}, err
	}
	history, err := s.store.History(target.ID)
	if err != nil {
		return RevisionDiff{}, err
	}
	var current, previous model.HistoryEntry
	for _, entry := range history {
		if entry.Hash == "" || (entry.Outcome != model.OutcomeBaseline && entry.Outcome != model.OutcomeChanged) {
			continue
		}
		if current.Hash == "" {
			current = entry
			continue
		}
		if entry.Hash != current.Hash {
			previous = entry
			break
		}
	}
	if current.Hash == "" || previous.Hash == "" {
		return RevisionDiff{}, fmt.Errorf("target %q does not have two distinct snapshots yet", name)
	}
	oldContent, err := s.store.Snapshot(previous.Hash)
	if err != nil {
		return RevisionDiff{}, err
	}
	newContent, err := s.store.Snapshot(current.Hash)
	if err != nil {
		return RevisionDiff{}, err
	}
	diff, err := compare.Build(oldContent, newContent, target.ResourceType, shortHash(previous.Hash), shortHash(current.Hash))
	if err != nil {
		return RevisionDiff{}, err
	}
	return RevisionDiff{Target: target, Current: current, Previous: previous, Diff: diff}, nil
}

func validateURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("URL must include a host")
	}
	if parsed.User != nil {
		return errors.New("credentials in URLs are not supported; use request headers")
	}
	return nil
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func errorFingerprint(err error) string {
	var fetchErr *fetch.Error
	if errors.As(err, &fetchErr) && fetchErr.Fingerprint != "" {
		return fetchErr.Fingerprint
	}
	return "unknown"
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func shortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}
