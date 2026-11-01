package app_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestPlans: the dashboard's reads and the rollup's day reads are index
// searches on the store's own rows, under RLS and generic plans.
func TestPlans(t *testing.T) {
	t.Parallel()
	primary, pq := testkit.CapturingPool(t, infra.AppURL)
	readPool, rq := testkit.CapturingPool(t, infra.AppURL)
	f := newFixture(t, primary, postgres.ReadOnly(readPool))
	a := f.store(t)
	f.order(a, t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), orders.StatusDelivered, "eid", "EG-C")
	f.rollup(a, t)
	if _, err := f.svc.Profit(a, day("2026-10-01"), day("2026-11-01"), domain.ByCampaign); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		capture *testkit.SQLCapture
		sql     string
	}{
		{rq, "dim = 'total' AND day >= $2 AND day < $3\nORDER BY day"},
		{rq, "dim = $2 AND day >= $3"},
		{pq, "FROM analytics_orders WHERE store_id = $1 AND day = $2"},
	} {
		testkit.AssertIndexCond(t, testkit.GenericPlan(a, t, primary, q.capture.Last(t, q.sql)), "store_id")
	}
}
