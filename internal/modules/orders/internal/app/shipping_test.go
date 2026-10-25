package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// fakeShipping delivers to Alexandria for 60 (free from 500) with a 15
// COD fee, in 2–4 days; not to Aswan; Luxor's quote fails. It keeps the
// last request.
type fakeShipping struct{ last *shipping.QuoteRequest }

func (f fakeShipping) Quote(_ context.Context, req shipping.QuoteRequest) (shipping.Quote, error) {
	*f.last = req
	zero, err := money.Zero(req.Subtotal.Currency())
	if err != nil {
		return shipping.Quote{}, err
	}
	switch req.Governorate {
	case "EG-ASN":
		return shipping.Quote{Shipping: zero, CODFee: zero}, nil
	case "EG-LX":
		return shipping.Quote{}, errors.New("shipping down")
	}
	ship, err := money.New(6000, req.Subtotal.Currency())
	if err != nil {
		return shipping.Quote{}, err
	}
	free := req.Subtotal.Amount() >= 50000
	if free {
		ship = zero
	}
	cod, err := money.New(1500, req.Subtotal.Currency())
	freeOver := int64(50000)
	return shipping.Quote{Deliverable: true, Shipping: ship, CODFee: cod, FreeShipping: free, FreeOverMinor: &freeOver,
		Delivery: &shipping.DeliveryDays{Min: 2, Max: 4}}, err
}

func withShipping(t *testing.T) (fixture, *shipping.QuoteRequest) {
	t.Helper()
	f := newFixture(t)
	last := &shipping.QuoteRequest{}
	f.env.Shipping = fakeShipping{last: last}
	f.svc = f.env.Service(f.pool)
	return f, last
}

// TestPlaceOrderWithShipping: the order's shipping and COD fee are the
// quote for its address and subtotal, and the total includes them, as
// stored and read back.
func TestPlaceOrderWithShipping(t *testing.T) {
	t.Parallel()
	f, last := withShipping(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)

	p, err := f.svc.PlaceOrder(f.shopperA, newOrder("ship-1", domain.LineInput{VariantID: tee, Qty: 2}))
	if err != nil {
		t.Fatal(err)
	}
	o := p.Order
	if o.Subtotal.Amount() != 30000 || o.Shipping.Amount() != 6000 || o.CODFee.Amount() != 1500 || o.Total.Amount() != 37500 {
		t.Fatalf("totals %+v", o)
	}
	if last.Governorate != "EG-ALX" || last.City == "" || last.Subtotal.Amount() != 30000 {
		t.Fatalf("quoted %+v", *last)
	}
	got, err := f.svc.Get(f.ctxA, o.ID)
	if err != nil || got.Shipping.Amount() != 6000 || got.CODFee.Amount() != 1500 || got.Total.Amount() != 37500 {
		t.Fatalf("read back %+v %v", got, err)
	}

	free, err := f.svc.PlaceOrder(f.shopperA, newOrder("ship-2", domain.LineInput{VariantID: tee, Qty: 4}))
	if err != nil || free.Order.Shipping.Amount() != 0 || free.Order.Total.Amount() != 61500 {
		t.Fatalf("free shipping: %+v %v", free.Order, err)
	}
}

// TestPlaceOrderNotDelivered: an address outside the store's zones is a
// validation error on the governorate and leaves nothing behind.
func TestPlaceOrderNotDelivered(t *testing.T) {
	t.Parallel()
	f, _ := withShipping(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	in := newOrder("ship-3", domain.LineInput{VariantID: tee, Qty: 1})
	in.Governorate = "EG-ASN"
	_, err := f.svc.PlaceOrder(f.shopperA, in)
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation || len(e.Fields) != 1 || e.Fields[0].Field != "governorate" || e.Fields[0].Code != "not_delivered" {
		t.Fatalf("err = %v", err)
	}
	if onHand, held := f.stock(f.ctxA, t, tee); onHand != 10 || held != 0 {
		t.Fatalf("stock after refusal: on hand %d, held %d", onHand, held)
	}
	in.Governorate = "EG-LX"
	if _, err = f.svc.PlaceOrder(f.shopperA, in); err == nil || errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("shipping down: %v", err)
	}
}

func TestQuote(t *testing.T) {
	t.Parallel()
	f, last := withShipping(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	q, err := f.svc.Quote(f.shopperA, domain.QuoteInput{Governorate: "EG-ALX", City: " سموحة ", Lines: []domain.LineInput{{VariantID: tee, Qty: 2}}})
	if err != nil || !q.Deliverable || q.Subtotal.Amount() != 30000 || q.Shipping.Amount() != 6000 || q.CODFee.Amount() != 1500 || q.Total.Amount() != 37500 {
		t.Fatalf("quote %+v %v", q, err)
	}
	if last.City != "سموحة" {
		t.Fatalf("city sent %q, want it cleaned", last.City)
	}
	if q.Delivery == nil || *q.Delivery != (shipping.DeliveryDays{Min: 2, Max: 4}) || q.FreeOverMinor == nil || *q.FreeOverMinor != 50000 {
		t.Fatalf("quote days %+v and threshold %v: shipping's must pass through", q.Delivery, q.FreeOverMinor)
	}
	q, err = f.svc.Quote(f.shopperA, domain.QuoteInput{Governorate: "EG-ASN", Lines: []domain.LineInput{{VariantID: tee, Qty: 2}}})
	if err != nil || q.Deliverable || q.Total.Amount() != 30000 {
		t.Fatalf("not delivered: %+v %v", q, err)
	}
	for name, in := range map[string]domain.QuoteInput{
		"unknown governorate": {Governorate: "EG-XX", Lines: []domain.LineInput{{VariantID: tee, Qty: 1}}},
		"no lines":            {Governorate: "EG-C"},
		"bad qty":             {Governorate: "EG-C", Lines: []domain.LineInput{{VariantID: tee, Qty: 0}}},
		"unsellable variant":  {Governorate: "EG-C", Lines: []domain.LineInput{{VariantID: f.env.IDs.New(), Qty: 1}}},
	} {
		if _, err = f.svc.Quote(f.shopperA, in); errs.KindOf(err) != errs.KindValidation {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Without a shipping module, nothing is charged.
	plain := newFixture(t)
	mug := plain.product(t, plain.storeA, "مج", 4950, 3)
	if q, err = plain.svc.Quote(plain.shopperA, domain.QuoteInput{Governorate: "EG-C", Lines: []domain.LineInput{{VariantID: mug, Qty: 1}}}); err != nil ||
		!q.Deliverable || !q.Shipping.IsZero() || q.Total.Amount() != 4950 {
		t.Fatalf("no shipping: %+v %v", q, err)
	}
	var _ app.Shipping = fakeShipping{}
}
