package app_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestMeters: the billing page's meters (ADR-0061), in order, with this
// month's counts and the modules' holdings, against the grants.
func TestMeters(t *testing.T) {
	t.Parallel()
	grants := app.Fixed{domain.OrdersPerMonth: {Value: 30}, domain.Messages: {Value: 100}, domain.Products: {Value: 50},
		domain.Staff: {Unlimited: true}, domain.CustomDomains: {Value: 1}}
	f := newFixture(t, grants)
	inTx := false
	count := func(n int64) app.Counter {
		return func(_ context.Context, tx pgx.Tx) (int64, error) {
			inTx = tx != nil
			return n, nil
		}
	}
	f.svc = app.NewService(app.Deps{DB: f.pool, Source: grants, Now: f.clock.now,
		Counters: map[domain.Feature]app.Counter{domain.Products: count(12), domain.Staff: count(3)}})
	ctx := f.store(t)
	if _, err := f.consume(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := f.svc.CountTx(ctx, tx, domain.OrdersPerMonth, 4)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ms, err := f.svc.Meters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []app.Meter{
		{Feature: domain.OrdersPerMonth, Kind: domain.Monthly, Used: 4, Limit: 30},
		{Feature: domain.Messages, Kind: domain.Monthly, Used: 7, Limit: 100},
		{Feature: domain.Products, Kind: domain.Count, Used: 12, Limit: 50},
		{Feature: domain.Staff, Kind: domain.Count, Used: 3, Unlimited: true},
	}
	if len(ms) != len(want) || !inTx {
		t.Fatalf("meters = %+v (counted in a tx: %v)", ms, inTx)
	}
	for i := range want {
		if ms[i] != want[i] {
			t.Fatalf("meter %d = %+v, want %+v", i, ms[i], want[i])
		}
	}
	// Another store counts its own month.
	other, err := f.svc.Meters(f.store(t))
	if err != nil || other[0].Used != 0 || other[1].Used != 0 {
		t.Fatalf("another store = %+v, %v", other, err)
	}
}
