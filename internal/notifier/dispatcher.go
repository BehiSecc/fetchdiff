package notifier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/model"
	"github.com/BehiSecc/fetchdiff/internal/store"
)

const deliveryLease = 10 * time.Minute

type DispatchReport struct {
	Claimed   int
	Delivered int
	Pending   int
	Errors    []error
}

func (r DispatchReport) Err() error { return errors.Join(r.Errors...) }

type Dispatcher struct {
	client *Client
	store  *store.Store
	now    func() time.Time
	owner  string
}

func NewDispatcher(client *Client, state *store.Store) *Dispatcher {
	return &Dispatcher{
		client: client,
		store:  state,
		now:    func() time.Time { return time.Now().UTC() },
		owner:  randomOwner(),
	}
}

func (d *Dispatcher) Drain(ctx context.Context) DispatchReport {
	now := d.now()
	events, err := d.store.ClaimNotifications(now, d.owner, deliveryLease, 50)
	if err != nil {
		return DispatchReport{Errors: []error{err}}
	}
	report := DispatchReport{Claimed: len(events)}
	for _, event := range events {
		d.drainEvent(ctx, event, &report)
		if err := d.store.ReleaseNotification(event.ID, d.owner); err != nil {
			report.Errors = append(report.Errors, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	_, report.Pending, err = d.store.NotificationCounts()
	if err != nil {
		report.Errors = append(report.Errors, err)
	}
	return report
}

func (d *Dispatcher) drainEvent(ctx context.Context, event model.Notification, report *DispatchReport) {
	chunks := SplitMessage(event.Text, ChunkLimit)
	keys := make([]string, 0, len(event.Deliveries))
	for key := range event.Deliveries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := event.Deliveries[key]
		if state.NextAttemptAt.After(d.now()) {
			continue
		}
		if !d.client.Has(key) {
			d.fail(event.ID, key, &state, fmt.Errorf("notification destination %q is no longer configured", key), report)
			continue
		}
		failed := false
		for state.NextChunk < len(chunks) {
			data := make(map[string]string, len(event.Data)+3)
			for dataKey, value := range event.Data {
				data[dataKey] = value
			}
			data["event"] = event.Kind
			data["target"] = event.TargetName
			data["chunk"] = fmt.Sprintf("%d", state.NextChunk+1)
			if err := d.client.Send(ctx, key, Message{Text: chunks[state.NextChunk], Data: data}); err != nil {
				d.fail(event.ID, key, &state, err, report)
				failed = true
				break
			}
			state.NextChunk++
			state.Attempts = 0
			state.LastError = ""
			state.LastAttemptAt = d.now()
			state.NextAttemptAt = time.Time{}
			if state.NextChunk < len(chunks) {
				if err := d.store.SaveDelivery(event.ID, d.owner, key, &state); err != nil {
					report.Errors = append(report.Errors, err)
					failed = true
					break
				}
			}
		}
		if failed {
			continue
		}
		if err := d.store.SaveDelivery(event.ID, d.owner, key, nil); err != nil {
			report.Errors = append(report.Errors, err)
			continue
		}
		report.Delivered++
	}
}

func (d *Dispatcher) fail(eventID, key string, state *model.DeliveryState, deliveryErr error, report *DispatchReport) {
	state.Attempts++
	state.LastAttemptAt = d.now()
	state.NextAttemptAt = state.LastAttemptAt.Add(retryDelay(state.Attempts))
	state.LastError = deliveryErr.Error()
	if err := d.store.SaveDelivery(eventID, d.owner, key, state); err != nil {
		report.Errors = append(report.Errors, errors.Join(deliveryErr, err))
		return
	}
	report.Errors = append(report.Errors, fmt.Errorf("%s: %w", key, deliveryErr))
}

func retryDelay(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}
	if attempt <= 0 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

func randomOwner() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
