package app_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
)

// TestJobs runs the backfill and the purge on River, as the worker does.
func TestJobs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.store(t)
	p := f.uniquePhone(t)
	o := f.order(a, p, 30, orders.StatusDelivered, orders.ReasonNone)

	workers := river.NewWorkers()
	if err := f.svc.AddWorkers(workers); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddWorkers(workers); err == nil {
		t.Fatal("registering twice must fail")
	}
	rc, err := river.NewClient(riverpgxv5.New(f.pool), &river.Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Workers: workers, Middleware: jobs.Middleware(), TestOnly: true,
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	done, cancel := rc.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()
	runCtx := context.WithoutCancel(t.Context())
	if err = rc.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(runCtx, 10*time.Second)
		defer stopCancel()
		_ = rc.Stop(stop)
	})
	ta, err := jobs.ForTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(kind string) {
		t.Helper()
		timeout := time.After(20 * time.Second)
		for {
			select {
			case ev := <-done:
				if ev.Job.Kind != kind {
					continue
				}
				if ev.Kind != river.EventKindJobCompleted || ev.Job.State != rivertype.JobStateCompleted {
					t.Fatalf("%s: %s %v", kind, ev.Kind, ev.Job.Errors)
				}
				return
			case <-timeout:
				t.Fatalf("timed out waiting for %s", kind)
			}
		}
	}
	if _, err = rc.Insert(a, app.BackfillArgs{TenantArgs: ta}, nil); err != nil {
		t.Fatal(err)
	}
	wait("risk.backfill")
	r, err := f.svc.ForOrder(a, o.ID)
	if err != nil || r.History.Orders != 1 {
		t.Fatalf("after the backfill job: %+v, %v", r, err)
	}
	if _, err = rc.Insert(t.Context(), app.PurgeArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	wait("risk.purge")
	if app.PurgePeriodic() == nil {
		t.Fatal("no periodic purge")
	}
}
