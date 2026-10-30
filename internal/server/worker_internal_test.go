package server

import (
	"testing"
	"time"

	"github.com/riverqueue/river"
)

// TestFailedJobRetention (P6 review): River keeps a cancelled or
// discarded job three days, not its default seven; a finished one keeps
// River's day.
func TestFailedJobRetention(t *testing.T) {
	t.Parallel()
	c := workerConfig(nil, nil, river.NewWorkers(), nil)
	if c.CancelledJobRetentionPeriod != 72*time.Hour || c.DiscardedJobRetentionPeriod != 72*time.Hour {
		t.Fatalf("retention: cancelled %v, discarded %v", c.CancelledJobRetentionPeriod, c.DiscardedJobRetentionPeriod)
	}
	if c.CompletedJobRetentionPeriod != 0 { // River's default (24h)
		t.Fatalf("completed retention changed: %v", c.CompletedJobRetentionPeriod)
	}
}
