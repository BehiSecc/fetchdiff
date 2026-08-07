package schedule

import (
	"testing"
	"time"

	"github.com/fetchdiff/fetchdiff/internal/model"
)

func TestNextWake(t *testing.T) {
	now := time.Now()
	targets := []model.Target{{NextCheckAt: now.Add(time.Hour)}, {NextCheckAt: now.Add(5 * time.Second)}}
	if got := NextWake(targets, now, 30*time.Second); got != 5*time.Second {
		t.Fatalf("wait = %s", got)
	}
	if !Due(model.Target{NextCheckAt: now}, now) {
		t.Fatal("target should be due")
	}
}
