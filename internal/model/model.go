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
	Revision             uint64            `json:"revision"`
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
	Recovered          bool      `json:"recovered,omitempty"`
	Error              string    `json:"error,omitempty"`
}

type Notification struct {
	ID         string                   `json:"id"`
	Kind       string                   `json:"kind"`
	TargetID   string                   `json:"target_id"`
	TargetName string                   `json:"target_name"`
	CreatedAt  time.Time                `json:"created_at"`
	Text       string                   `json:"text"`
	Data       map[string]string        `json:"data,omitempty"`
	Deliveries map[string]DeliveryState `json:"deliveries"`
	LeaseOwner string                   `json:"lease_owner,omitempty"`
	LeaseUntil time.Time                `json:"lease_until,omitempty"`
}

type DeliveryState struct {
	NextChunk     int       `json:"next_chunk"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}
