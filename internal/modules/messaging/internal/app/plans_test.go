package app_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestDashboardQueryPlans (P6 review) seeds a large store: 20,000
// messages this month, 20 of them failed, one order with three, and
// 3,000 notices of which 5 are unread. As app_user under generic plans:
//   - one status's page reads that status's index, without a sort or a
//     scan, however rare the status;
//   - an order's messages are found by the order, from its time on;
//   - the bell's unread count reads the unread notices only.
func TestDashboardQueryPlans(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	store, _ := tenant.FromContext(ctx)
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	order := ulid.NewGenerator().New()
	seed := []string{`
INSERT INTO messages (store_id, created_at, id, kind, locale, order_id, status, to_masked)
SELECT $1, date_trunc('month', now()) + n * interval '1 second',
       (lpad(to_hex((extract(epoch FROM date_trunc('month', now()) + n * interval '1 second') * 1000)::bigint), 12, '0') ||
        substr(md5(n::text || 'm'), 1, 20))::uuid,
       'order_confirmation', 'ar', md5(n::text)::uuid,
       CASE WHEN n % 1000 = 0 THEN 'failed' ELSE 'read' END, '+20 10•• ••• 678'
FROM generate_series(1, 20000) AS n`, `
INSERT INTO messages (store_id, created_at, id, kind, locale, order_id, status, to_masked)
SELECT $1, now(), (lpad(to_hex((extract(epoch FROM now()) * 1000)::bigint), 12, '0') || substr(md5(n::text || 'o'), 1, 20))::uuid,
       'order_confirmation', 'ar', $2, 'delivered', '+20 10•• ••• 678'
FROM generate_series(1, 3) AS n`, `
INSERT INTO merchant_notifications (store_id, id, kind, dedup_key, read_at)
SELECT $1, md5(n::text || 'n')::uuid, 'order_confirmed', 'k' || n, CASE WHEN n % 600 = 0 THEN NULL ELSE now() END
FROM generate_series(1, 3000) AS n`}
	if err = postgres.InTenantTx(ctx, owner, func(tx pgx.Tx) error {
		for i, q := range seed {
			args := []any{store.ID}
			if i == 1 {
				args = append(args, order)
			}
			if _, execErr := tx.Exec(ctx, q, args...); execErr != nil {
				return execErr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "merchant_notifications"} {
		if _, err = owner.Exec(t.Context(), "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}

	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	svc := app.NewService(app.Deps{DB: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	page, err := svc.Messages(ctx, nil, domain.Failed, 50)
	if err != nil || len(page.Items) != 20 {
		t.Fatalf("failed page: %d, %v", len(page.Items), err)
	}
	plan := testkit.GenericPlan(ctx, t, pool, capture.Last(t, "status = $2"))
	testkit.AssertIndexCond(t, plan, "status = ")
	if strings.Contains(plan, "Seq Scan") || strings.Contains(plan, "Sort") {
		t.Errorf("a status's page sorts or scans:\n%s", plan)
	}
	t.Log("\n" + plan)

	got, err := svc.OrderMessages(ctx, order)
	if err != nil || len(got) != 3 {
		t.Fatalf("order messages: %d, %v", len(got), err)
	}
	plan = testkit.GenericPlan(ctx, t, pool, capture.Last(t, "order_id = $2"))
	testkit.AssertIndexCond(t, plan, "order_id = ")
	if strings.Contains(plan, "Seq Scan") {
		t.Errorf("an order's messages scan a partition:\n%s", plan)
	}

	notes, err := svc.Notifications(ctx, nil, false, 10)
	if err != nil || notes.Unread != 5 {
		t.Fatalf("unread = %d, %v", notes.Unread, err)
	}
	plan = testkit.GenericPlan(ctx, t, pool, capture.Last(t, "read_at IS NULL"))
	if !strings.Contains(plan, "merchant_notifications_unread") {
		t.Errorf("the unread count does not read the unread index:\n%s", plan)
	}
	t.Log("\n" + plan)
}
