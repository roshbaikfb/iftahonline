package app_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestBestsellers: confirmed orders count their units per product, once
// however often the counting job runs; a cancellation takes them back;
// pending orders and other stores do not count; only the last 30 days do.
func TestBestsellers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := f.product(t, f.storeA, "تيشيرت", 15000, 50)
	mug := f.product(t, f.storeA, "مج", 4950, 50)
	hat := f.product(t, f.storeA, "كاب", 9900, 50)
	place := func(key string, lines ...domain.LineInput) ulid.ID {
		t.Helper()
		o := newOrder(key, lines...)
		o.Phone = "0101234" + key[len(key)-4:]
		p, err := f.svc.PlaceOrder(f.shopperA, o)
		if err != nil {
			t.Fatal(err)
		}
		return p.Order.ID
	}
	confirm := func(id ulid.ID) {
		t.Helper()
		if _, err := f.svc.Transition(f.ctxA, id, domain.StatusConfirmed, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	count := func(id ulid.ID) {
		t.Helper()
		for range 2 { // a job may run twice
			if err := f.svc.CountSales(f.shopperA, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := place("sale-0001", domain.LineInput{VariantID: tee, Qty: 3}, domain.LineInput{VariantID: mug, Qty: 1})
	b := place("sale-0002", domain.LineInput{VariantID: mug, Qty: 1})
	c := place("sale-0003", domain.LineInput{VariantID: hat, Qty: 5}) // stays pending
	productOf := map[ulid.ID]ulid.ID{}                                // variant → product, from the orders' lines
	for _, id := range []ulid.ID{a, c} {
		o, err := f.svc.Get(f.ctxA, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range o.Lines {
			productOf[l.VariantID] = l.ProductID
		}
	}
	for _, id := range []ulid.ID{a, b} {
		confirm(id)
	}
	for _, id := range []ulid.ID{a, b, c} {
		count(id)
	}
	top, err := f.svc.Bestsellers(f.shopperA, 10)
	if err != nil || len(top) != 2 || top[0] != productOf[tee] || top[1] != productOf[mug] {
		t.Fatalf("bestsellers %v %v: want the tee (3) then the mug (2); the pending caps do not count", top, err)
	}
	if other, otherErr := f.svc.Bestsellers(f.ctxB, 10); otherErr != nil || len(other) != 0 {
		t.Fatalf("another store's bestsellers: %v %v", other, otherErr)
	}
	if _, err = f.svc.Transition(f.ctxA, a, domain.StatusCancelled, "", 0); err != nil {
		t.Fatal(err)
	}
	count(a)
	if top, err = f.svc.Bestsellers(f.shopperA, 10); err != nil || len(top) != 1 || top[0] != productOf[mug] {
		t.Fatalf("after cancelling: %v %v (only the other order's mug)", top, err)
	}
	if top, err = f.svc.Bestsellers(f.shopperA, 1); err != nil || len(top) != 1 {
		t.Fatalf("limit: %v %v", top, err)
	}
	f.svc.SetClock(func() time.Time { return time.Now().Add(31 * 24 * time.Hour) })
	if top, err = f.svc.Bestsellers(f.shopperA, 10); err != nil || len(top) != 0 {
		t.Fatalf("a month later: %v %v", top, err)
	}
}
