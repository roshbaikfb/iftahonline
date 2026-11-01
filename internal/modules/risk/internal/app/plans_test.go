package app_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestPlans: the store's lookups are index searches as app_user under the
// generic plan, and the platform-wide read is one too, as the table's
// owner runs it inside app.risk_signals (ADR-0065).
func TestPlans(t *testing.T) {
	t.Parallel()
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	f := newFixtureOn(t, pool)
	a := f.store(t)
	o := f.order(a, f.uniquePhone(t), 1, orders.StatusDelivered, orders.ReasonNone)
	if err := f.svc.Learn(a, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ForOrder(a, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Bands(a, []ulid.ID{o.ID}); err != nil {
		t.Fatal(err)
	}
	keys := capture.Last(t, "FROM unnest($1::uuid[]) AS ids(id)")
	testkit.AssertIndexCond(t, testkit.GenericPlan(a, t, pool, keys), "order_id")
	history := capture.Last(t, "FROM risk_orders\nWHERE store_id = $1 AND phone_key = $2")
	testkit.AssertIndexCond(t, testkit.GenericPlan(a, t, pool, history), "phone_key")

	// The function's own query, as its owner.
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	var plan strings.Builder
	if err = postgres.InTx(t.Context(), owner, func(tx pgx.Tx) error {
		if _, xErr := tx.Exec(t.Context(), "SET LOCAL enable_seqscan = off"); xErr != nil {
			return xErr
		}
		rows, qErr := tx.Query(t.Context(), `EXPLAIN (COSTS OFF)
			SELECT o.store_id, o.outcome, o.outcome_at FROM public.risk_orders o
			WHERE o.phone_key = $1 AND o.outcome IS NOT NULL AND o.outcome_at >= now() - interval '730 days'
			ORDER BY o.outcome_at DESC LIMIT 200`, make([]byte, 32))
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if qErr = rows.Scan(&line); qErr != nil {
				return qErr
			}
			plan.WriteString(line + "\n")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	testkit.AssertIndexCond(t, plan.String(), "phone_key")
	if !strings.Contains(plan.String(), "risk_orders_phone_outcome") {
		t.Fatalf("not the phone index:\n%s", plan.String())
	}
}
