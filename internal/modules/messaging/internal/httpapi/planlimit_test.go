package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestPlanLimitNotice: a plan-limit notice carries its feature and
// numbers to the dashboard's bell.
func TestPlanLimitNotice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctxA, `INSERT INTO merchant_notifications (store_id, id, kind, dedup_key, data)
			VALUES (app.current_store_id(), $1, 'plan_limit_nearly', 'orders_per_month:2026-10',
			        '{"feature":"orders_per_month","used":240,"limit":300,"period":"2026-10"}')`, f.ids.New())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, page := f.do(t, req(t, http.MethodGet, path(f.storeA, "/messaging/notifications"), tokSupportA, ""))
	items, _ := page["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("notices = %d %v", code, page)
	}
	n := items[0].(map[string]any)
	if n["kind"] != "plan_limit_nearly" || n["feature"] != "orders_per_month" || n["used"] != float64(240) || n["limit"] != float64(300) {
		t.Fatalf("notice = %v", n)
	}
}
