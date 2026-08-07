package model

import "time"

const (
	ResourceJavaScript = "JavaScript"
	ResourceHTML       = "HTML"
	ResourceText       = "Text"

	OutcomeBaseline  = "baseline"
	OutcomeChanged   = "changed"
	OutcomeUnchanged = "unchanged"
	OutcomeFailure   = "failure"
	OutcomeRecovery  = "recovery"
)

type Target struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	URL                  string            `json:"url"`
	Every                time.Duration     `json:"every"`
	Headers              map[string]string `json:"headers,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	NextCheckAt          time.Time         `json:"next_check_at"`
	LastCheckedAt        time.Time         `json:"last_checked_at,omitempty"`
	LastSuccessAt        time.Time         `json:"last_success_at,omitempty"`
	LastChangedAt        time.Time         `json:"last_changed_at,omitempty"`
	SnapshotHash         string            `json:"snapshot_hash"`
	SnapshotSize         int64             `json:"snapshot_size"`
	ContentType          string            `json:"content_type,omitempty"`
	ResourceType         string            `json:"resource_type"`
	ETag                 string            `json:"etag,omitempty"`
	LastModified         string            `json:"last_modified,omitempty"`
	EffectiveURL         string            `json:"effective_url"`
	StatusCode           int               `json:"status_code"`
	ConsecutiveFailures  int               `json:"consecutive_failures"`
	LastErrorFingerprint string            `json:"last_error_fingerprint,omitempty"`
	FailureReported      bool              `json:"failure_reported"`
	LastError            string            `json:"last_error,omitempty"`
}

type HistoryEntry struct {
	ID                 string    `json:"id"`
	TargetID           string    `json:"target_id"`
	CheckedAt          time.Time `json:"checked_at"`
	Outcome            string    `json:"outcome"`
	Hash               string    `json:"hash,omitempty"`
	PreviousHash       string    `json:"previous_hash,omitempty"`
	Size               int64     `json:"size"`
	PreviousSize       int64     `json:"previous_size,omitempty"`
	StatusCode         int       `json:"status_code,omitempty"`
	PreviousStatusCode int       `json:"previous_status_code,omitempty"`
	EffectiveURL       string    `json:"effective_url,omitempty"`
	PreviousURL        string    `json:"previous_url,omitempty"`
	StatusChanged      bool      `json:"status_changed,omitempty"`
	RedirectChanged    bool      `json:"redirect_changed,omitempty"`
	Error              string    `json:"error,omitempty"`
}
