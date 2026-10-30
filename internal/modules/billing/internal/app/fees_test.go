package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// orderBook is a fake orders module: the status each order has now.
type orderBook struct {
	mu     sync.Mutex
	orders map[ulid.ID]app.Order
}

func (b *orderBook) set(id ulid.ID, o app.Order) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.orders[id] = o
}

func (b *orderBook) get(_ context.Context, _ pgx.Tx, id ulid.ID) (app.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.orders[id], nil
}

type feeRow struct {
	period          time.Time
	base, fee       int64
	rate            int32
	status          string
	returned        bool
	planIsPayAsSell bool
}

func (f fixture) fees(ctx context.Context, t *testing.T) map[ulid.ID]feeRow {
	t.Helper()
	out := map[ulid.ID]feeRow{}
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT s.order_id, s.period, s.base_minor, s.fee_minor, s.rate_bps, s.status, s.returned_at IS NOT NULL, p.kind = 'pays'
			FROM sell_fees s JOIN plans p ON p.id = s.plan_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id ulid.ID
			var r feeRow
			if err = rows.Scan(&id, &r.period, &r.base, &r.fee, &r.rate, &r.status, &r.returned, &r.planIsPayAsSell); err != nil {
				return err
			}
			out[id] = r
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestSellFees: a pay-as-you-sell store pays a fee on each delivered
// order, once; a return before the invoice takes it back, after the
// invoice it becomes a credit; other plans pay none (ADR-0048).
func TestSellFees(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	book := &orderBook{orders: map[ulid.ID]app.Order{}}
	svc := app.NewService(app.Deps{DB: f.pool, Orders: book.get, Now: func() time.Time { return now }})
	pays, free, pro := f.store(t), f.store(t), f.store(t)
	f.subscribe(pays, t, "pays", "active", "")
	f.subscribe(pro, t, "pro", "active", "")
	deliveredAt := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)

	kept, returned, late := f.ids.New(), f.ids.New(), f.ids.New()
	for _, id := range []ulid.ID{kept, returned, late} {
		book.set(id, app.Order{Status: app.OrderDelivered, Base: egp(t, 100000), StatusAt: deliveredAt})
		for range 2 { // a retried job
			if err := svc.OrderMoved(pays, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := f.fees(pays, t)
	if len(got) != 3 {
		t.Fatalf("%d fees, want 3", len(got))
	}
	if r := got[kept]; r.fee != 2500 || r.base != 100000 || r.rate != 250 || r.status != "due" || !r.planIsPayAsSell ||
		!r.period.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("fee = %+v", r)
	}

	// One is returned before the month's invoice, one after it.
	if err := postgres.InTenantTx(pays, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(pays, `UPDATE sell_fees SET status = 'invoiced' WHERE order_id = $1`, late)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ulid.ID{returned, late} {
		book.set(id, app.Order{Status: app.OrderReturned, Base: egp(t, 100000), StatusAt: deliveredAt.AddDate(0, 0, 3)})
		for range 2 {
			if err := svc.OrderMoved(pays, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	got = f.fees(pays, t)
	if got[returned].status != "cancelled" || !got[returned].returned || got[late].status != "credit_due" || got[kept].status != "due" {
		t.Fatalf("after returns = %+v", got)
	}

	// No fee on other plans, nor for an order that is not delivered, nor
	// a return without a fee.
	other := f.ids.New()
	book.set(other, app.Order{Status: app.OrderDelivered, Base: egp(t, 100000), StatusAt: deliveredAt})
	for _, ctx := range []context.Context{free, pro} {
		if err := svc.OrderMoved(ctx, other); err != nil {
			t.Fatal(err)
		}
		if n := len(f.fees(ctx, t)); n != 0 {
			t.Fatalf("%d fees on a plan without one", n)
		}
	}
	pending, back := f.ids.New(), f.ids.New()
	book.set(pending, app.Order{Status: app.OrderOther})
	book.set(back, app.Order{Status: app.OrderReturned, StatusAt: deliveredAt})
	for _, id := range []ulid.ID{pending, back, f.ids.New()} {
		if err := svc.OrderMoved(pays, id); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.fees(pays, t)); n != 3 {
		t.Fatalf("%d fees, want still 3", n)
	}
}

func TestOrderMovedNeedsOrders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.svc.OrderMoved(f.store(t), f.ids.New()); err == nil {
		t.Fatal("no orders: want an error")
	}
}
