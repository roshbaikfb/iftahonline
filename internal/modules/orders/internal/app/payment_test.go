package app_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestCardOrders: a storefront card order awaits the gateway and pays no
// COD fee; staff orders are COD; the gateway's word sets the payment
// status once, with an audit entry and an event; COD orders ignore it.
func TestCardOrders(t *testing.T) {
	t.Parallel()
	f, _ := withShipping(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	in := newOrder("card-1", domain.LineInput{VariantID: tee, Qty: 2})
	in.PaymentMethod = domain.PaymentCard
	p, err := f.svc.PlaceOrder(f.shopperA, in)
	if err != nil {
		t.Fatal(err)
	}
	o := p.Order
	if o.PaymentMethod != domain.PaymentCard || o.PaymentStatus != domain.PaymentAwaiting || !o.CODFee.IsZero() ||
		o.Shipping.Amount() != 6000 || o.Total.Amount() != 36000 {
		t.Fatalf("card order %+v", o)
	}
	if q, qErr := f.svc.Quote(f.shopperA, domain.QuoteInput{Governorate: "EG-ALX", Lines: in.Lines, PaymentMethod: domain.PaymentCard}); qErr != nil ||
		!q.CODFee.IsZero() || q.Total.Amount() != 36000 {
		t.Fatalf("card quote %+v %v", q, qErr)
	}
	merchant := merchantOrder(domain.LineInput{VariantID: tee, Qty: 1})
	merchant.PaymentMethod = domain.PaymentCard
	if _, err = f.svc.PlaceOrder(f.ctxA, merchant); !hasField(err, "payment_method", "invalid") {
		t.Fatalf("staff card order: %v", err)
	}
	bad := newOrder("card-2", domain.LineInput{VariantID: tee, Qty: 1})
	bad.PaymentMethod = "cheque"
	if _, err = f.svc.PlaceOrder(f.shopperA, bad); !hasField(err, "payment_method", "invalid") {
		t.Fatalf("unknown method: %v", err)
	}

	f.audit.Take()
	f.events.Take()
	set := func(orderID ulid.ID, st domain.PaymentStatus) bool {
		var changed bool
		if txErr := postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) (setErr error) {
			changed, setErr = f.svc.SetPaymentStatusTx(f.shopperA, tx, orderID, st, "gateway:paymob")
			return setErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return changed
	}
	if !set(o.ID, domain.PaymentPaid) || set(o.ID, domain.PaymentPaid) {
		t.Fatal("paid once, then a no-op")
	}
	got, err := f.svc.Get(f.ctxA, o.ID)
	if err != nil || got.PaymentStatus != domain.PaymentPaid {
		t.Fatalf("after paid: %+v %v", got, err)
	}
	if a := f.audit.Take(); len(a) != 1 || a[0] != "order.payment_paid "+o.ID.String() {
		t.Fatalf("audit %v", a)
	}
	if e := f.events.Take(); len(e) != 1 {
		t.Fatalf("events %v", e)
	}
	cod, err := f.svc.PlaceOrder(f.shopperA, newOrder("cod-1", domain.LineInput{VariantID: tee, Qty: 1}))
	if err != nil || cod.Order.PaymentMethod != domain.PaymentCOD || cod.Order.PaymentStatus != domain.PaymentUnpaid || cod.Order.CODFee.Amount() != 1500 {
		t.Fatalf("cod order %+v %v", cod.Order, err)
	}
	if set(cod.Order.ID, domain.PaymentPaid) {
		t.Fatal("a COD order took a gateway's payment status")
	}
	err = postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
		_, setErr := f.svc.SetPaymentStatusTx(f.shopperA, tx, o.ID, "lost", "gateway:paymob")
		return setErr
	})
	if err == nil {
		t.Fatal("an invalid status accepted")
	}
	err = postgres.InTenantTx(f.ctxB, f.pool, func(tx pgx.Tx) error {
		_, setErr := f.svc.SetPaymentStatusTx(f.ctxB, tx, o.ID, domain.PaymentRefunded, "gateway:paymob")
		return setErr
	})
	if !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
}
