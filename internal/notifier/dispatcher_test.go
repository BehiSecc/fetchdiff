package notifier

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestCanceledDispatcherReleasesAllClaims(t *testing.T) {
	client, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	state := dispatcherStore(t)
	now := time.Now().UTC()
	queueNotification(t, state, now)
	target, err := state.Target("target-1")
	if err != nil {
		t.Fatal(err)
	}
	expected := target.Revision
	target.Revision++
	second := model.Notification{
		ID: "event-2", Kind: model.OutcomeChanged, TargetID: target.ID, TargetName: target.Name, CreatedAt: now.Add(time.Second),
		Text: "changed again", Deliveries: map[string]model.DeliveryState{"custom:webhook": {}},
	}
	if err := state.CommitTarget(target, expected, nil, []model.Notification{second}); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(client, state)
	dispatcher.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dispatcher.Drain(ctx)
	notifications, err := state.Notifications()
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatalf("notifications = %#v", notifications)
	}
	for _, notification := range notifications {
		if notification.LeaseOwner != "" || !notification.LeaseUntil.IsZero() {
			t.Fatalf("notification remained leased: %#v", notification)
		}
	}
}

func TestDispatcherResumesAtFailedChunk(t *testing.T) {
	var requests atomic.Int32
	var delivered strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := requests.Add(1)
		content, _ := io.ReadAll(request.Body)
		if call == 2 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		delivered.Write(content)
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
	target := model.Target{ID: "target-1", Revision: 1, Name: "app", URL: "https://example.com/app.js"}
	if err := state.CreateTarget(target, model.HistoryEntry{TargetID: target.ID, CheckedAt: now, Outcome: model.OutcomeBaseline}); err != nil {
		t.Fatal(err)
	}
	target.Revision++
	message := strings.Repeat("change-line\n", 200)
	event := model.Notification{
		ID: "event-1", Kind: model.OutcomeChanged, TargetID: target.ID, TargetName: target.Name, CreatedAt: now,
		Text: message, Deliveries: map[string]model.DeliveryState{"custom:webhook": {}},
	}
	if err := state.CommitTarget(target, 1, nil, []model.Notification{event}); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(client, state)
	dispatcher.now = func() time.Time { return now }
	first := dispatcher.Drain(context.Background())
	if first.Err() == nil {
		t.Fatal("expected second chunk failure")
	}
	pending, err := state.Notifications()
	if err != nil {
		t.Fatal(err)
	}
	if got := pending[0].Deliveries["custom:webhook"].NextChunk; got != 1 {
		t.Fatalf("next chunk = %d, want 1", got)
	}
	dispatcher.now = func() time.Time { return now.Add(31 * time.Second) }
	second := dispatcher.Drain(context.Background())
	if second.Err() != nil || second.Delivered != 1 {
		t.Fatalf("second report = %#v", second)
	}
	if requests.Load() != 4 {
		t.Fatalf("requests = %d, want 4", requests.Load())
	}
	if strings.Count(delivered.String(), "FetchDiff · 1/3") != 1 {
		t.Fatalf("first chunk was duplicated:\n%s", delivered.String())
	}
}

func TestDispatcherKeepsOnlyFailedDestinationPending(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "later", http.StatusServiceUnavailable) }))
	defer bad.Close()
	client, err := New(Config{Custom: []*custom.Options{
		{ID: "good", CustomWebhookURL: good.URL, CustomMethod: http.MethodPost, CustomFormat: "{{data}}"},
		{ID: "bad", CustomWebhookURL: bad.URL, CustomMethod: http.MethodPost, CustomFormat: "{{data}}"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := dispatcherStore(t)
	now := time.Now().UTC()
	target := model.Target{ID: "target-1", Revision: 1, Name: "app", URL: "https://example.com/app.js"}
	if err := state.CreateTarget(target, model.HistoryEntry{TargetID: target.ID, CheckedAt: now, Outcome: model.OutcomeBaseline}); err != nil {
		t.Fatal(err)
	}
	target.Revision++
	event := model.Notification{
		ID: "event-1", CreatedAt: now, Text: "changed",
		Deliveries: map[string]model.DeliveryState{"custom:good": {}, "custom:bad": {}},
	}
	if err := state.CommitTarget(target, 1, nil, []model.Notification{event}); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(client, state)
	dispatcher.now = func() time.Time { return now }
	report := dispatcher.Drain(context.Background())
	if report.Delivered != 1 || report.Pending != 1 || report.Err() == nil {
		t.Fatalf("report = %#v", report)
	}
	pending, err := state.Notifications()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || len(pending[0].Deliveries) != 1 {
		t.Fatalf("pending = %#v", pending)
	}
	if _, ok := pending[0].Deliveries["custom:bad"]; !ok {
		t.Fatalf("wrong destination remained: %#v", pending[0].Deliveries)
	}
}
