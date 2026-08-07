package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/config"
	"github.com/BehiSecc/fetchdiff/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := New(paths)
	if err := store.Initialize(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSnapshotRoundTripAndDeduplication(t *testing.T) {
	store := newTestStore(t)
	content := []byte("const answer=42;")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	if err := store.PutSnapshot(hash, content); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSnapshot(hash, content); err != nil {
		t.Fatal(err)
	}
	got, err := store.Snapshot(hash)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("snapshot = %q, want %q", got, content)
	}
	path := filepath.Join(store.Paths().Snapshots, hash[:2], hash+".gz")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", got)
	}
}

func TestTargetAndHistory(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	target := model.Target{ID: "target-1", Name: "Production-JS", URL: "https://example.com/app.js"}
	entry := model.HistoryEntry{TargetID: target.ID, CheckedAt: now, Outcome: model.OutcomeBaseline}
	if err := store.CreateTarget(target, entry); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTarget(model.Target{ID: "target-2", Name: "production-js"}, entry); err == nil {
		t.Fatal("expected case-insensitive duplicate name error")
	}
	got, err := store.Target("production-js")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != target.ID {
		t.Fatalf("target ID = %q, want %q", got.ID, target.ID)
	}
	history, err := store.History(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Outcome != model.OutcomeBaseline {
		t.Fatalf("history = %#v", history)
	}
}

func TestUpdateTargetRejectsStaleRevision(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	target := model.Target{ID: "target-1", Revision: 1, Name: "app", URL: "https://example.com/app.js"}
	entry := model.HistoryEntry{TargetID: target.ID, CheckedAt: now, Outcome: model.OutcomeBaseline}
	if err := store.CreateTarget(target, entry); err != nil {
		t.Fatal(err)
	}
	target.Revision = 2
	if err := store.UpdateTarget(target, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTarget(target, 1); !errors.Is(err, ErrTargetChanged) {
		t.Fatalf("stale update error = %v", err)
	}
}
