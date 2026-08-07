package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/config"
	"github.com/BehiSecc/fetchdiff/internal/fetch"
	"github.com/BehiSecc/fetchdiff/internal/model"
	"github.com/BehiSecc/fetchdiff/internal/store"
)

type fetchStep struct {
	response fetch.Response
	err      error
}

type fakeFetcher struct {
	steps    []fetchStep
	requests []fetch.Request
}

func (f *fakeFetcher) Fetch(_ context.Context, request fetch.Request) (fetch.Response, error) {
	f.requests = append(f.requests, request)
	if len(f.steps) == 0 {
		return fetch.Response{}, errors.New("no fake response")
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	return step.response, step.err
}

func newTestService(t *testing.T, fetcher Fetcher, destinations ...string) (*Service, *store.Store) {
	t.Helper()
	paths, err := config.ResolvePaths(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	state := store.New(paths)
	if err := state.Initialize(); err != nil {
		t.Fatal(err)
	}
	return New(state, fetcher, destinations...), state
}

func baselineResponse(content string) fetch.Response {
	return fetch.Response{
		StatusCode:   http.StatusOK,
		Status:       "200 OK",
		EffectiveURL: "https://cdn.example.com/app.js",
		ContentType:  "application/javascript",
		ETag:         `"v1"`,
		LastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
		Content:      []byte(content),
	}
}

func TestAddCreatesBaselineAndConditionalChange(t *testing.T) {
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse(`function value(){return 1}`)},
		{response: baselineResponse(`function value(){return 2}`)},
	}}
	service, state := newTestService(t, fake)
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	target, err := service.Add(context.Background(), AddInput{
		Name: "production-js", URL: "https://cdn.example.com/app.js", Every: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ResourceType != model.ResourceJavaScript || target.NextCheckAt != now.Add(24*time.Hour) {
		t.Fatalf("target = %#v", target)
	}
	if _, err := state.Snapshot(target.SnapshotHash); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	result, err := service.CheckTarget(context.Background(), target.Name, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.PreviousHash != target.SnapshotHash {
		t.Fatalf("result = %#v", result)
	}
	if fake.requests[1].ETag != `"v1"` || fake.requests[1].LastModified == "" {
		t.Fatalf("conditional request = %#v", fake.requests[1])
	}
	diff, err := service.RevisionDiff(target.Name)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Diff.Added == 0 || diff.Diff.Removed == 0 {
		t.Fatalf("diff = %#v", diff.Diff)
	}
}

func TestNotModifiedKeepsSnapshot(t *testing.T) {
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse("hello")},
		{response: fetch.Response{StatusCode: http.StatusNotModified, Status: "304 Not Modified", EffectiveURL: "https://cdn.example.com/app.js", NotModified: true}},
	}}
	service, _ := newTestService(t, fake)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	target, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	result, err := service.CheckTarget(context.Background(), target.Name, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Target.SnapshotHash != target.SnapshotHash {
		t.Fatalf("result = %#v", result)
	}
}

func TestFailuresDoNotReplaceSnapshotAndRecoveryIsRecorded(t *testing.T) {
	failure := &fetch.Error{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Fingerprint: "http:500", Err: errors.New("server error")}
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse("good")},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: baselineResponse("good")},
	}}
	service, _ := newTestService(t, fake)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	target, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		now = now.Add(time.Minute)
		result, err := service.CheckTarget(context.Background(), target.Name, true)
		if err == nil {
			t.Fatal("expected fetch failure")
		}
		if result.Target.SnapshotHash != target.SnapshotHash {
			t.Fatal("failure replaced the snapshot")
		}
		if result.FailureReached != (i == FailureThreshold) {
			t.Fatalf("failure %d reached = %v", i, result.FailureReached)
		}
	}
	now = now.Add(time.Minute)
	result, err := service.CheckTarget(context.Background(), target.Name, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Recovered || result.History.Outcome != model.OutcomeRecovery {
		t.Fatalf("recovery result = %#v", result)
	}
}

func TestAddAcceptsEmptySuccessfulContent(t *testing.T) {
	fake := &fakeFetcher{steps: []fetchStep{{response: fetch.Response{
		StatusCode: http.StatusNoContent, Status: "204 No Content", EffectiveURL: "https://example.com/empty",
	}}}}
	service, _ := newTestService(t, fake)
	target, err := service.Add(context.Background(), AddInput{Name: "empty", URL: "https://example.com/empty", Every: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if target.SnapshotSize != 0 || target.SnapshotHash != contentHash(nil) {
		t.Fatalf("target = %#v", target)
	}
}

func TestRevisionDiffIgnoresFailureRows(t *testing.T) {
	failure := &fetch.Error{StatusCode: 500, Status: "500 Internal Server Error", Fingerprint: "http:500", Err: errors.New("server error")}
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse("const value=1")},
		{response: baselineResponse("const value=2")},
		{response: fetch.Response{StatusCode: 500}, err: failure},
	}}
	service, _ := newTestService(t, fake)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	if _, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := service.CheckTarget(context.Background(), "app", true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := service.CheckTarget(context.Background(), "app", true); err == nil {
		t.Fatal("expected fetch failure")
	}
	revision, err := service.RevisionDiff("app")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Current.Outcome != model.OutcomeChanged || revision.Current.StatusCode != http.StatusOK {
		t.Fatalf("current revision = %#v", revision.Current)
	}
}

type overlappingFetcher struct {
	calls    atomic.Int32
	entered  atomic.Int32
	ready    chan struct{}
	closeOne sync.Once
}

func (f *overlappingFetcher) Fetch(_ context.Context, _ fetch.Request) (fetch.Response, error) {
	call := f.calls.Add(1)
	if call == 1 {
		return baselineResponse("const value=1"), nil
	}
	if call == 2 || call == 3 {
		if f.entered.Add(1) == 2 {
			f.closeOne.Do(func() { close(f.ready) })
		}
		<-f.ready
		if call == 2 {
			return baselineResponse("const value=2"), nil
		}
	}
	return baselineResponse("const value=3"), nil
}

func TestOverlappingChecksCannotRollBackState(t *testing.T) {
	fake := &overlappingFetcher{ready: make(chan struct{})}
	service, state := newTestService(t, fake)
	service.now = func() time.Time { return time.Now().UTC() }
	if _, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.CheckTarget(context.Background(), "app", true)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	target, err := state.Target("app")
	if err != nil {
		t.Fatal(err)
	}
	if target.SnapshotHash != contentHash([]byte("const value=3")) {
		t.Fatalf("final hash = %s", target.SnapshotHash)
	}
}

func TestChangedCheckQueuesFullNotificationAtomically(t *testing.T) {
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse(`function value(){return 1}`)},
		{response: baselineResponse(`function value(){return 2}`)},
	}}
	service, state := newTestService(t, fake, "custom:webhook")
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	if _, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := service.CheckTarget(context.Background(), "app", true); err != nil {
		t.Fatal(err)
	}
	notifications, err := state.Notifications()
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("notifications = %#v", notifications)
	}
	if !strings.Contains(notifications[0].Text, "return 2") || !strings.Contains(notifications[0].Text, "@@") {
		t.Fatalf("notification does not contain the full diff:\n%s", notifications[0].Text)
	}
	if _, ok := notifications[0].Deliveries["custom:webhook"]; !ok {
		t.Fatalf("deliveries = %#v", notifications[0].Deliveries)
	}
}

func TestFailureThresholdAndRecoveryQueueOnce(t *testing.T) {
	failure := &fetch.Error{StatusCode: 500, Status: "500 Internal Server Error", Fingerprint: "http:500", Err: errors.New("server error")}
	fake := &fakeFetcher{steps: []fetchStep{
		{response: baselineResponse("good")},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: fetch.Response{StatusCode: 500}, err: failure},
		{response: baselineResponse("good")},
	}}
	service, state := newTestService(t, fake, "custom:webhook")
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	if _, err := service.Add(context.Background(), AddInput{Name: "app", URL: "https://cdn.example.com/app.js", Every: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		now = now.Add(time.Minute)
		_, _ = service.CheckTarget(context.Background(), "app", true)
	}
	events, _, err := state.NotificationCounts()
	if err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("failure events = %d, want 1", events)
	}
	now = now.Add(time.Minute)
	if _, err := service.CheckTarget(context.Background(), "app", true); err != nil {
		t.Fatal(err)
	}
	events, _, err = state.NotificationCounts()
	if err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("events after recovery = %d, want 2", events)
	}
}
