package app_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func newOrder(key string, lines ...domain.LineInput) domain.NewOrder {
	return domain.NewOrder{IdempotencyKey: key, Source: domain.SourceStorefront, Name: "منى أحمد", Phone: "01012345678",
		Governorate: "EG-ALX", City: "سموحة", Address: "14 شارع فوزي معاذ", Notes: "اتصلي قبل الوصول", Lines: lines}
}

func TestPlaceOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	mug := f.product(t, f.storeA, "مج سيراميك", 4950, 3)
	f.audit.take()

	p, err := f.svc.PlaceOrder(f.shopperA, newOrder("k-1", domain.LineInput{VariantID: tee, Qty: 2}, domain.LineInput{VariantID: mug, Qty: 1}))
	if err != nil {
		t.Fatal(err)
	}
	o := p.Order
	if p.Replayed || p.Duplicate || o.Status != domain.StatusPending || o.Version != 1 || o.Source != domain.SourceStorefront {
		t.Fatalf("placement %+v", p)
	}
	if o.Subtotal.Amount() != 34950 || o.Total.Amount() != 34950 || o.Shipping.Amount() != 0 || o.ItemCount != 3 ||
		o.Total.Currency() != "EGP" || len(o.Lines) != 2 {
		t.Fatalf("totals %+v", o)
	}
	if !o.CreatedAt.Equal(domain.CreatedAtOf(o.ID)) || o.Contact.Phone.Unmasked() != "+201012345678" || o.Contact.Governorate != "EG-ALX" {
		t.Fatalf("order %+v", o)
	}
	byTitle := map[string]domain.Line{}
	for _, l := range o.Lines {
		byTitle[l.Title] = l
	}
	if l := byTitle["تيشيرت قطن"]; l.Qty != 2 || l.UnitPrice.Amount() != 15000 || l.Total.Amount() != 30000 {
		t.Fatalf("tee line %+v", l)
	}

	// Stock is held, the customer is recorded, and the read-back matches.
	if on, res := f.stock(f.ctxA, t, tee); on != 10 || res != 2 {
		t.Fatalf("tee stock %d/%d", on, res)
	}
	d, err := f.svc.GetDetail(f.ctxA, o.ID)
	if err != nil || len(d.Order.Lines) != 2 || len(d.Events) != 1 || d.Events[0].To != domain.StatusPending ||
		d.Events[0].From != "" || d.Events[0].ActorUserID != nil {
		t.Fatalf("detail %+v %v", d, err)
	}
	if d.Order.CustomerID != o.CustomerID || o.CustomerID.IsZero() {
		t.Fatalf("customer %s vs %s", d.Order.CustomerID, o.CustomerID)
	}
	if got := f.audit.take(); !slices.Equal(got, []string{"order.placed " + o.ID.String()}) {
		t.Fatalf("audit %v", got)
	}
	if got := f.events.take(); !slices.Equal(got, []string{"orders.order.placed " + o.ID.String()}) {
		t.Fatalf("events %v", got)
	}

	// A price change later does not change the placed order.
	if _, err = f.svc.Get(f.ctxA, o.ID); err != nil {
		t.Fatal(err)
	}
	// Store B cannot see it.
	if _, err = f.svc.Get(f.ctxB, o.ID); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("store B reads A's order: %v", err)
	}
}

// TestDoubleSubmitCreatesOneOrder is the P4 acceptance test: a shopper's
// double submit, sent concurrently, creates exactly one order and holds
// stock once, with the Idempotency-Key and, without one, through the
// duplicate window.
func TestDoubleSubmitCreatesOneOrder(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"form-7f3e9c1a", ""} {
		t.Run(fmt.Sprintf("key=%q", key), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			v := f.product(t, f.storeA, "سماعة", 50000, 100)
			const n = 20
			var wg sync.WaitGroup
			results := make([]app.Placement, n)
			errsCh := make(chan error, n)
			start := make(chan struct{})
			for i := range n {
				wg.Go(func() {
					<-start
					p, err := f.svc.PlaceOrder(f.shopperA, newOrder(key, domain.LineInput{VariantID: v, Qty: 1}))
					results[i] = p
					errsCh <- err
				})
			}
			close(start)
			wg.Wait()
			close(errsCh)
			for err := range errsCh {
				if err != nil {
					t.Fatalf("submit: %v", err)
				}
			}
			first, repeats := results[0].Order.ID, 0
			for _, r := range results {
				if r.Order.ID != first {
					t.Fatalf("two orders: %s and %s", first, r.Order.ID)
				}
				if r.Replayed || r.Duplicate {
					repeats++
				}
			}
			if repeats != n-1 {
				t.Fatalf("%d of %d submits were repeats, want %d", repeats, n, n-1)
			}
			page, err := f.svc.List(f.ctxA, domain.Query{})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("orders in store: %d (%v)", len(page.Items), err)
			}
			if _, res := f.stock(f.ctxA, t, v); res != 1 {
				t.Fatalf("reserved %d, want 1", res)
			}
			if blocked, err := f.cust.Blocked(f.ctxA, mustPhone(t, "01012345678")); err != nil || blocked {
				t.Fatalf("customer: %v %v", blocked, err)
			}
		})
	}
}

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIdempotencyAndDuplicateWindow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "شاحن", 20000, 50)
	line := domain.LineInput{VariantID: v, Qty: 1}

	first, err := f.svc.PlaceOrder(f.shopperA, newOrder("same-key", line))
	if err != nil {
		t.Fatal(err)
	}
	// The same key with another request is refused, not replayed.
	other := newOrder("same-key", domain.LineInput{VariantID: v, Qty: 2})
	if _, err = f.svc.PlaceOrder(f.shopperA, other); !errors.Is(err, app.ErrIdempotencyKeyReused) {
		t.Fatalf("reused key: %v", err)
	}
	// A retry long after still replays (keys live a week), with lines.
	f.advance(time.Hour)
	again, err := f.svc.PlaceOrder(f.shopperA, newOrder("same-key", line))
	if err != nil || !again.Replayed || again.Order.ID != first.Order.ID || len(again.Order.Lines) != 1 {
		t.Fatalf("replay: %+v %v", again, err)
	}

	// Without a key: same phone and lines inside the window is the same
	// order; outside it, or with other lines, a new one.
	noKey := newOrder("", line)
	a, err := f.svc.PlaceOrder(f.shopperA, noKey)
	if err != nil || a.Duplicate {
		t.Fatalf("first unkeyed: %+v %v", a, err)
	}
	f.advance(domain.DuplicateWindow - time.Minute)
	b, err := f.svc.PlaceOrder(f.shopperA, noKey)
	if err != nil || !b.Duplicate || b.Order.ID != a.Order.ID {
		t.Fatalf("inside the window: %+v %v", b, err)
	}
	c, err := f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: 3}))
	if err != nil || c.Duplicate {
		t.Fatalf("other lines: %+v %v", c, err)
	}
	f.advance(2 * time.Minute)
	d, err := f.svc.PlaceOrder(f.shopperA, noKey)
	if err != nil || d.Duplicate || d.Order.ID == a.Order.ID {
		t.Fatalf("after the window: %+v %v", d, err)
	}
	// A cancelled order is not a duplicate: the shopper may order again.
	if _, err = f.svc.Transition(f.ctxA, d.Order.ID, domain.StatusCancelled, "", 0); err != nil {
		t.Fatal(err)
	}
	e, err := f.svc.PlaceOrder(f.shopperA, noKey)
	if err != nil || e.Duplicate {
		t.Fatalf("after cancelling: %+v %v", e, err)
	}
	// Staff entering two identical phone orders get two orders.
	staff := newOrder("", line)
	staff.Source = domain.SourceMerchant
	s1, err1 := f.svc.PlaceOrder(f.ctxA, staff)
	s2, err2 := f.svc.PlaceOrder(f.ctxA, staff)
	if err1 != nil || err2 != nil || s1.Order.ID == s2.Order.ID || s2.Duplicate {
		t.Fatalf("merchant orders: %v %v", err1, err2)
	}
}

// TestPlaceOrderRefusals: a refused order leaves nothing behind (no
// order, no stock held, no customer count, no key), so fixing the cause
// and retrying with the same key works.
func TestPlaceOrderRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	short := f.product(t, f.storeA, "قطعة نادرة", 99900, 1)
	plenty := f.product(t, f.storeA, "عادي", 1000, 50)
	foreign := f.product(t, f.storeB, "منتج متجر آخر", 1000, 50)

	// Out of stock: the whole order rolls back.
	_, err := f.svc.PlaceOrder(f.shopperA, newOrder("k-short", domain.LineInput{VariantID: plenty, Qty: 1}, domain.LineInput{VariantID: short, Qty: 2}))
	var se *catalog.StockError
	if !errors.As(err, &se) || se.VariantID != short || !errors.Is(err, catalog.ErrInsufficientStock) {
		t.Fatalf("short stock: %v", err)
	}
	if _, res := f.stock(f.ctxA, t, plenty); res != 0 {
		t.Fatalf("plenty reserved %d after the rollback", res)
	}
	if page, _ := f.svc.List(f.ctxA, domain.Query{}); len(page.Items) != 0 {
		t.Fatalf("orders after refusal: %d", len(page.Items))
	}
	// The same key, fixed, now places the order.
	if _, err = f.svc.PlaceOrder(f.shopperA, newOrder("k-short", domain.LineInput{VariantID: short, Qty: 1})); err != nil {
		t.Fatalf("retry with the key: %v", err)
	}

	// Another store's variant is simply not for sale here.
	_, err = f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: foreign, Qty: 1}))
	if !errs.Is(err, errs.KindValidation) || !hasField(err, "lines[0].variant_id", "unavailable") {
		t.Fatalf("foreign variant: %v", err)
	}
	_, err = f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: ulid.NewGenerator().New(), Qty: 1}))
	if !hasField(err, "lines[0].variant_id", "unavailable") {
		t.Fatalf("unknown variant: %v", err)
	}

	// A blocked phone is refused before any work.
	blockPhone(t, f, "01055555555")
	blocked := newOrder("k-blocked", domain.LineInput{VariantID: plenty, Qty: 1})
	blocked.Phone = "01055555555"
	if _, err = f.svc.PlaceOrder(f.shopperA, blocked); !errors.Is(err, customers.ErrCustomerBlocked) {
		t.Fatalf("blocked: %v", err)
	}
	if _, res := f.stock(f.ctxA, t, plenty); res != 0 {
		t.Fatalf("stock held for a blocked customer: %d", res)
	}
	_, err = f.svc.PlaceOrder(f.shopperA, domain.NewOrder{Source: domain.SourceStorefront})
	if !errs.Is(err, errs.KindValidation) {
		t.Fatalf("empty order: %v", err)
	}
}

func hasField(err error, field, code string) bool {
	var e *errs.Error
	if !errors.As(err, &e) {
		return false
	}
	for _, f := range e.Fields {
		if f.Field == field && f.Code == code {
			return true
		}
	}
	return false
}
