package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// planOf is the store's plan code and status now.
func (f fixture) planOf(ctx context.Context, t *testing.T) (code, status string, trialUsed bool) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.code, s.status, s.trial_used FROM subscriptions s JOIN plans p ON p.id = s.plan_id`).Scan(&code, &status, &trialUsed)
	}); err != nil {
		t.Fatal(err)
	}
	return code, status, trialUsed
}

// TestEndPeriods: a trial that ended and a cancelled period that ended
// move to the free plan, each with SubscriptionChanged; a running trial
// and a paid period stay (ADR-0050).
func TestEndPeriods(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	jobs, events := testkit.NewJobs(), testkit.NewEvents()
	svc := app.NewService(app.Deps{DB: f.pool, Jobs: jobs, Events: events, IDs: f.ids, Now: func() time.Time { return now }})
	ended, running, cancelled, paid := f.store(t), f.store(t), f.store(t), f.store(t)
	past, future := now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)
	f.subscribe(ended, t, "pro", "trialing", "trial_ends_at, trial_used='"+past+"', true")
	f.subscribe(running, t, "pro", "trialing", "trial_ends_at, trial_used='"+future+"', true")
	f.subscribe(cancelled, t, "merchant", "active", "cycle, period_start, period_end, cancel_at_period_end='monthly', '2026-10-01T00:00:00Z', '"+past+"', true")
	f.subscribe(paid, t, "merchant", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '"+past+"'")

	if err := (&app.EndPeriodsWorker{Svc: svc}).Work(t.Context(), &river.Job[app.EndPeriodsArgs]{}); err != nil {
		t.Fatal(err)
	}
	asked := map[tenant.StoreID]bool{}
	for _, a := range jobs.Args {
		if j, ok := a.(app.EndPeriodArgs); ok {
			asked[j.StoreID] = true
		}
	}
	for ctx, want := range map[context.Context]bool{ended: true, running: false, cancelled: true, paid: false} {
		id, _ := tenant.FromContext(ctx)
		if asked[id] != want {
			t.Errorf("store %s asked = %v, want %v", id, asked[id], want)
		}
	}
	for _, ctx := range []context.Context{ended, running, cancelled, paid} {
		for range 2 { // a retried job
			if err := (&app.EndPeriodWorker{Svc: svc}).Work(ctx, &river.Job[app.EndPeriodArgs]{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for ctx, want := range map[context.Context]string{ended: "free/active", running: "pro/trialing", cancelled: "free/active", paid: "merchant/active"} {
		code, status, _ := f.planOf(ctx, t)
		if code+"/"+status != want {
			t.Errorf("plan = %s/%s, want %s", code, status, want)
		}
	}
	if _, _, used := f.planOf(ended, t); !used {
		t.Error("the trial is used, after it ended too")
	}
	all := events.All()
	if len(all) != 2 {
		t.Fatalf("%d events, want 2", len(all))
	}
	for _, ev := range all {
		if c, ok := ev.(domain.SubscriptionChanged); !ok || c.Status != domain.Active || c.Type != domain.EventSubscriptionChanged {
			t.Errorf("event = %+v", ev)
		}
	}
	if app.EndPeriodsPeriodic() == nil || (app.EndPeriodsArgs{}).Kind() != "billing.end_periods" || (app.EndPeriodArgs{}).Kind() != "billing.end_period" {
		t.Error("periodic job or kinds")
	}
	if err := app.NewService(app.Deps{DB: f.pool}).EndPeriods(t.Context()); err == nil {
		t.Error("EndPeriods without jobs")
	}
}
