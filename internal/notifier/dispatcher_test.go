package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/config"
	"github.com/BehiSecc/fetchdiff/internal/model"
	"github.com/BehiSecc/fetchdiff/internal/store"
	"github.com/projectdiscovery/notify/pkg/providers/custom"
)

func dispatcherStore(t *testing.T) *store.Store {
	t.Helper()
	paths, err := config.ResolvePaths(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	state := store.New(paths)
	if err := state.Initialize(); err != nil {
		t.Fatal(err)
	}
	return state
}

func queueNotification(t *testing.T, state *store.Store, now time.Time) {
	t.Helper()
	target := model.Target{ID: "target-1", Revision: 1, Name: "app", URL: "https://example.com/app.js"}
	if err := state.CreateTarget(target, model.HistoryEntry{TargetID: target.ID, CheckedAt: now, Outcome: model.OutcomeBaseline}); err != nil {
		t.Fatal(err)
	}
	target.Revision++
	event := model.Notification{
		ID: "event-1", Kind: model.OutcomeChanged, TargetID: target.ID, TargetName: target.Name, CreatedAt: now,
		Text: "changed", Deliveries: map[string]model.DeliveryState{"custom:webhook": {}},
	}
	if err := state.CommitTarget(target, 1, nil, []model.Notification{event}); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherRetriesWithoutDroppingEvent(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := New(Config{Custom: []*custom.Options{{
		ID: "webhook", CustomWebhookURL: server.URL, CustomMethod: http.MethodPost, CustomFormat: "{{data}}",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	state := dispatcherStore(t)
	now := time.Now().UTC()
	queueNotification(t, state, now)
	dispatcher := NewDispatcher(client, state)
	dispatcher.now = func() time.Time { return now }
	first := dispatcher.Drain(context.Background())
	if first.Err() == nil || first.Pending != 1 {
		t.Fatalf("first report = %#v", first)
	}
	fail.Store(false)
	dispatcher.now = func() time.Time { return now.Add(31 * time.Second) }
	second := dispatcher.Drain(context.Background())
	if second.Err() != nil || second.Delivered != 1 || second.Pending != 0 {
		t.Fatalf("second report = %#v", second)
	}
}
