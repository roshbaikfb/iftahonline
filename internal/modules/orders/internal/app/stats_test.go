package app_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// The dashboard home's sums (the owner, 2026-10-27): sales are every
// order but cancelled and returned ones, which are summed apart; the
// previous period is the same span one period earlier.
func TestStats(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := f.product(t, f.storeA, "تيشيرت", 10000, 50)
	place := func(i int) domain.Order {
		t.Helper()
		in := merchantOrder(domain.LineInput{VariantID: tee, Qty: i + 1})
		in.Phone = fmt.Sprintf("0101111111%d", i)
		p, err := f.svc.PlaceOrder(f.ctxA, in)
		if err != nil {
			t.Fatal(err)
		}
		return p.Order
	}
	move := func(o domain.Order, to ...domain.Status) {
		t.Helper()
		for _, st := range to {
			if _, err := f.svc.Transition(f.ctxA, o.ID, st, "", 0); err != nil {
				t.Fatalf("%s: %v", st, err)
			}
		}
	}
	pending, confirmed, cancelled, returned := place(0), place(1), place(2), place(3)
	move(confirmed, domain.StatusConfirmed)
	move(cancelled, domain.StatusCancelled)
	move(returned, domain.StatusConfirmed, domain.StatusReadyToShip, domain.StatusShipped, domain.StatusDelivered, domain.StatusReturned)
	// The confirmed one was paid by card.
	if err := postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctxA, `UPDATE orders SET payment_method = 'card' WHERE id = $1`, confirmed.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Store B's order never counts for A.
	if _, err := f.svc.PlaceOrder(f.ctxB, merchantOrder(domain.LineInput{VariantID: f.product(t, f.storeB, "B", 99900, 5), Qty: 1})); err != nil {
		t.Fatal(err)
	}

	st, err := f.svc.Stats(f.ctxA, domain.RangeToday)
	if err != nil {
		t.Fatal(err)
	}
	sales := pending.Total.Amount() + confirmed.Total.Amount()
	if st.Sales.Orders != 2 || st.Sales.TotalMinor != sales || st.COD != 1 || st.Card != 1 || st.Currency != "EGP" {
		t.Fatalf("sales = %+v cod %d card %d %s; want 2 orders, %d", st.Sales, st.COD, st.Card, st.Currency, sales)
	}
	if st.Cancelled != (domain.Tally{Orders: 1, TotalMinor: cancelled.Total.Amount()}) || st.Returned != (domain.Tally{Orders: 1, TotalMinor: returned.Total.Amount()}) {
		t.Fatalf("cancelled %+v returned %+v", st.Cancelled, st.Returned)
	}
	if st.Previous != (domain.Tally{}) || st.OldestPending == nil || !st.OldestPending.Equal(pending.CreatedAt) {
		t.Fatalf("previous %+v oldest pending %v; want %v", st.Previous, st.OldestPending, pending.CreatedAt)
	}
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	last := st.Daily[len(st.Daily)-1]
	if len(st.Daily) != domain.ChartDays || last.Date != time.Now().In(cairo).Format(time.DateOnly) || last.Orders != 2 || last.SalesMinor != sales {
		t.Fatalf("daily: %d days, last %+v", len(st.Daily), last)
	}

	// A day later: nothing today; yesterday until the same hour is the
	// previous period; the 7 days still hold them.
	tomorrow := time.Now().Add(24 * time.Hour)
	f.svc.SetClock(func() time.Time { return tomorrow })
	if st, err = f.svc.Stats(f.ctxA, domain.RangeToday); err != nil || st.Sales != (domain.Tally{}) || st.Previous != (domain.Tally{Orders: 2, TotalMinor: sales}) {
		t.Fatalf("next day = sales %+v previous %+v, %v", st.Sales, st.Previous, err)
	}
	if st.Daily[len(st.Daily)-2].Orders != 2 || st.Daily[len(st.Daily)-1].Orders != 0 {
		t.Fatalf("the chart did not move a day: %+v", st.Daily[len(st.Daily)-2:])
	}
	if st, err = f.svc.Stats(f.ctxA, domain.Range7d); err != nil || st.Sales.Orders != 2 || st.Cancelled.Orders != 1 {
		t.Fatalf("7 days = %+v, %v", st, err)
	}

	// With none pending, there is no oldest.
	move(pending, domain.StatusConfirmed)
	if st, err = f.svc.Stats(f.ctxA, domain.Range30d); err != nil || st.OldestPending != nil {
		t.Fatalf("oldest pending = %v, %v", st.OldestPending, err)
	}
}
