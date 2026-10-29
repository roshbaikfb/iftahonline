package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeCoupons knows TEN (10% off), SHIP (free shipping), GONE
// (expired), and FLIP (10% when quoted, 20% when placed: its terms
// changed in between). It records each redemption.
type fakeCoupons struct {
	mu       sync.Mutex
	redeemed []string
}

func (f *fakeCoupons) apply(code string, subtotal money.Money, placing bool) (app.CouponDiscount, error) {
	pct := int64(0)
	switch code {
	case "TEN":
		pct = 10
	case "FLIP":
		pct = 10
		if placing {
			pct = 20
		}
	case "SHIP":
		zero, err := money.Zero(subtotal.Currency())
		return app.CouponDiscount{Amount: zero, FreeShipping: true}, err
	case "GONE":
		return app.CouponDiscount{}, &app.CouponRejected{Reason: "expired"}
	default:
		return app.CouponDiscount{}, &app.CouponRejected{Reason: "not_found"}
	}
	off, err := money.New(subtotal.Amount()*pct/100, subtotal.Currency())
	return app.CouponDiscount{Amount: off}, err
}

func (f *fakeCoupons) Preview(_ context.Context, code string, subtotal money.Money) (app.CouponDiscount, error) {
	return f.apply(code, subtotal, false)
}

func (f *fakeCoupons) RedeemTx(_ context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (app.CouponDiscount, error) {
	if tx == nil || customer.IsZero() || order.IsZero() {
		return app.CouponDiscount{}, errors.New("redeem outside the order's transaction")
	}
	d, err := f.apply(code, subtotal, true)
	if err == nil {
		f.mu.Lock()
		f.redeemed = append(f.redeemed, code+":"+order.String())
		f.mu.Unlock()
	}
	return d, err
}

func couponField(t *testing.T, err error) string {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || len(e.Fields) != 1 || e.Fields[0].Field != "coupon_code" {
		t.Fatalf("want a coupon_code field error, got %v", err)
	}
	return e.Fields[0].Code
}

// TestCoupons: a coupon's discount comes off the items in the quote and
// the placed order (total, stored, read back); free shipping waives the
// delivery rate; a code that does not apply, or changed since the
// quote, is a field error and places nothing; a replayed order redeems
// once.
func TestCoupons(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	coupons := &fakeCoupons{}
	f.env.Shipping = fakeShipping{last: &shipping.QuoteRequest{}} // Alexandria: 60, COD fee 15
	f.env.Coupons = coupons
	svc := f.env.Service(f.pool)
	tee := f.product(t, f.storeA, "تيشيرت", 15000, 20)
	line := domain.LineInput{VariantID: tee, Qty: 2} // 30000

	q, err := svc.Quote(f.shopperA, domain.QuoteInput{Governorate: "EG-ALX", Lines: []domain.LineInput{line}, CouponCode: " ten "})
	if err != nil || q.Discount.Amount() != 3000 || q.CouponCode != "TEN" || q.Total.Amount() != 30000-3000+6000+1500 {
		t.Fatalf("quote = %+v, %v", q, err)
	}
	in := newOrder("k-coupon", line)
	in.CouponCode = "ten"
	p, err := svc.PlaceOrder(f.shopperA, in)
	if err != nil {
		t.Fatal(err)
	}
	o := p.Order
	if o.Discount.Amount() != 3000 || o.CouponCode != "TEN" || o.Total.Amount() != 34500 || o.Shipping.Amount() != 6000 {
		t.Fatalf("order = discount %v code %q total %v", o.Discount, o.CouponCode, o.Total)
	}
	got, err := svc.Get(f.ctxA, o.ID)
	if err != nil || got.Discount.Amount() != 3000 || got.CouponCode != "TEN" || got.Total.Amount() != 34500 {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	// The same submit again: the first order, no second redemption.
	if p, err = svc.PlaceOrder(f.shopperA, in); err != nil || !p.Replayed || p.Order.ID != o.ID {
		t.Fatalf("replay = %+v, %v", p, err)
	}
	if len(coupons.redeemed) != 1 || coupons.redeemed[0] != "TEN:"+o.ID.String() {
		t.Fatalf("redeemed = %v", coupons.redeemed)
	}

	// Free shipping: the rate goes, the COD fee stays.
	ship := newOrder("k-ship", line)
	ship.CouponCode, ship.Phone = "SHIP", "01022222222"
	if p, err = svc.PlaceOrder(f.shopperA, ship); err != nil {
		t.Fatal(err)
	}
	if p.Order.Shipping.Amount() != 0 || p.Order.CODFee.Amount() != 1500 || p.Order.Discount.Amount() != 0 || p.Order.Total.Amount() != 31500 {
		t.Fatalf("free shipping = %+v", p.Order)
	}

	// Refusals place nothing.
	for code, want := range map[string]string{"GONE": "expired", "NOPE": "not_found", "FLIP": "changed", "WITH SPACE": "not_found"} {
		bad := newOrder("k-"+strings.ReplaceAll(code, " ", ""), line)
		bad.CouponCode, bad.Phone = code, "01033333333"
		_, err = svc.PlaceOrder(f.shopperA, bad)
		if got := couponField(t, err); got != want {
			t.Errorf("%s: %s, want %s", code, got, want)
		}
	}
	if _, err = svc.Quote(f.shopperA, domain.QuoteInput{Governorate: "EG-ALX", Lines: []domain.LineInput{line}, CouponCode: "GONE"}); couponField(t, err) != "expired" {
		t.Fatal("quote of an expired code")
	}
	// FLIP was redeemed, then refused as changed: in promotions its use
	// rolls back with the order's transaction (this fake only records).
	if len(coupons.redeemed) != 3 || !strings.HasPrefix(coupons.redeemed[2], "FLIP:") {
		t.Fatalf("redemptions = %v", coupons.redeemed)
	}
	if onHand, held := f.stock(f.ctxA, t, tee); onHand != 20 || held != 4 {
		t.Fatalf("stock = %d on hand, %d held (refusals must hold none)", onHand, held)
	}

	// Without the port, every code is unknown; no code is fine.
	plain := newFixture(t)
	v := plain.product(t, plain.storeA, "x", 1000, 5)
	in = newOrder("", domain.LineInput{VariantID: v, Qty: 1})
	in.CouponCode = "TEN"
	if _, err = plain.svc.PlaceOrder(plain.shopperA, in); couponField(t, err) != "not_found" {
		t.Fatal("a code without promotions")
	}
	in.CouponCode = strings.Repeat("A", 33)
	if _, err = plain.svc.PlaceOrder(plain.shopperA, in); couponField(t, err) != "not_found" {
		t.Fatal("a code too long")
	}
	in.CouponCode = ""
	if p, err = plain.svc.PlaceOrder(plain.shopperA, in); err != nil || p.Order.Discount.Amount() != 0 || p.Order.CouponCode != "" {
		t.Fatalf("no code = %+v, %v", p.Order, err)
	}
}
