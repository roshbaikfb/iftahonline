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

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
)

// TestJobs runs the schedule on River as the worker does: the periodic
// fan-out finds the store's dirty days and computes them; the sweep and
// the backfill run per store.
func TestJobs(t *testing.T) {
	t.Parallel()
	f := newDefault(t)
	a := f.store(t)
	f.now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	o := f.order(a, t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), orders.StatusDelivered, "eid", "EG-C")
	f.charges.m[o.ID] = egp(t, 7000)

	workers := river.NewWorkers()
	if err := f.svc.AddWorkers(workers); err != nil {
		t.Fatal(err)
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
	ta, err := jobs.ForTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rc.Insert(a, app.BackfillArgs{TenantArgs: ta}, nil); err != nil {
		t.Fatal(err)
	}
	wait("analytics.backfill")
	if _, err = rc.Insert(a, app.SweepArgs{TenantArgs: ta}, nil); err != nil {
		t.Fatal(err)
	}
	wait("analytics.sweep")
	// The fan-out queues a rollup for every store with dirty days, A
	// among them (through the service's own inserter).
	if _, err = rc.Insert(t.Context(), app.RollupsArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	wait("analytics.rollups")
	queued := false
	for _, j := range f.jobs.Args {
		if r, ok := j.(app.RollupArgs); ok && r.StoreID == ta.StoreID {
			queued = true
		}
	}
	if !queued {
		t.Fatalf("no rollup queued for store A: %v", f.jobs.Kinds())
	}
	if _, err = rc.Insert(a, app.RollupArgs{TenantArgs: ta}, nil); err != nil {
		t.Fatal(err)
	}
	wait("analytics.rollup")
	r, err := f.svc.Profit(a, day("2026-10-20"), day("2026-10-21"), domain.ByProduct)
	if err != nil || r.Totals.Delivered != 1 || r.Totals.ShippingCost != 7000 {
		t.Fatalf("after the jobs: %+v, %v", r.Totals, err)
	}
	if _, err = rc.Insert(t.Context(), app.SweepsArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	wait("analytics.sweeps")
	if len(app.Periodic()) != 2 {
		t.Fatal("two periodic jobs")
	}
}
