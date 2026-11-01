package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// oneOrder is a Fulfilment holding one order.
type oneOrder struct{ o orders.Order }

func (f oneOrder) Get(ctx context.Context, id ulid.ID) (orders.Order, error) {
	return f.GetTx(ctx, nil, id)
}

func (f oneOrder) GetTx(_ context.Context, _ pgx.Tx, id ulid.ID) (orders.Order, error) {
	if id != f.o.ID {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	return f.o, nil
}

func (oneOrder) TransitionSystemTx(context.Context, pgx.Tx, ulid.ID, orders.Status, string, string) (bool, error) {
	return false, nil
}

func (oneOrder) TransitionSystemReasonTx(context.Context, pgx.Tx, ulid.ID, orders.Status, string, string, orders.OutcomeReason) (bool, error) {
	return false, nil
}

func egpOf(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestBillingOrders: a fee's base is the goods after their discount, and
// only delivered and returned orders count.
func TestBillingOrders(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	o := orders.Order{ID: ulid.NewGenerator().New(), Subtotal: egpOf(t, 50000), Discount: egpOf(t, 5000), Shipping: egpOf(t, 4000),
		CODFee: egpOf(t, 1000), StatusChangedAt: at}
	for st, want := range map[orders.Status]billing.OrderStatus{
		orders.StatusDelivered: billing.OrderDelivered, orders.StatusReturned: billing.OrderReturned,
		orders.StatusShipped: billing.OrderOther, orders.StatusCancelled: billing.OrderOther,
	} {
		o.Status = st
		got, err := billingOrders(oneOrder{o})(t.Context(), nil, o.ID)
		if err != nil || got.Status != want {
			t.Fatalf("%s: %+v, %v", st, got, err)
		}
		if want != billing.OrderOther && (got.Base.Amount() != 45000 || got.Base.Currency() != money.EGP || !got.StatusAt.Equal(at)) {
			t.Errorf("%s: base %v at %v", st, got.Base, got.StatusAt)
		}
	}
	if got, err := billingOrders(oneOrder{o})(t.Context(), nil, ulid.NewGenerator().New()); err != nil || got.Status != billing.OrderOther {
		t.Errorf("a missing order: %+v, %v", got, err)
	}
}

// merchantSends records the merchant notices sent, failing to those in
// fail.
type merchantSends struct {
	got  []string
	fail map[string]bool
}

func (m *merchantSends) SendMerchant(_ context.Context, to string, k messaging.MerchantKind, locale string, values []string) error {
	if m.fail[to] {
		return errors.New("WhatsApp is down")
	}
	m.got = append(m.got, to+"|"+string(k)+"|"+locale+"|"+strings.Join(values, ";"))
	return nil
}

// TestOwnerWhatsAppNotices: each billing notice is its template with its
// values, to every owner's phone; a failure is logged, never returned
// (the email went, and a retry would send it again).
func TestOwnerWhatsAppNotices(t *testing.T) {
	t.Parallel()
	grace := time.Date(2026, 11, 8, 3, 0, 0, 0, time.UTC)
	sends := &merchantSends{fail: map[string]bool{"+201000000002": true}}
	o := ownerNotices{phones: func(context.Context) ([]string, error) { return []string{"+201000000001", "+201000000002"}, nil },
		whatsapp: sends, log: slog.New(slog.DiscardHandler)}
	for _, n := range []billing.Notice{
		{Kind: billing.NoticePaymentFailed, Invoice: "INV-2026-000001", TotalMinor: 68286, Currency: "EGP", GraceUntil: grace},
		{Kind: billing.NoticePaymentFailed, Invoice: "INV-2026-000002", TotalMinor: 100, Currency: "EGP"},
		{Kind: billing.NoticeSuspended},
		{Kind: billing.NoticePaid, Invoice: "INV-2026-000001", TotalMinor: 68286, Currency: "EGP"},
	} {
		o.send(t.Context(), n, "ar", "سوق")
	}
	want := []string{
		"+201000000001|billing_payment_failed|ar|سوق;INV-2026-000001;682.86 EGP;2026-11-08",
		"+201000000001|billing_payment_failed|ar|سوق;INV-2026-000002;1.00 EGP;-",
		"+201000000001|billing_store_suspended|ar|سوق",
		"+201000000001|billing_payment_received|ar|سوق;INV-2026-000001;682.86 EGP",
	}
	if strings.Join(sends.got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sent:\n%s", strings.Join(sends.got, "\n"))
	}
	// Without WhatsApp, or the phones unreadable: nothing, quietly.
	ownerNotices{}.send(t.Context(), billing.Notice{Kind: billing.NoticeSuspended}, "ar", "s")
	ownerNotices{phones: func(context.Context) ([]string, error) { return nil, errors.New("db") }, whatsapp: sends, log: slog.New(slog.DiscardHandler)}.
		send(t.Context(), billing.Notice{Kind: billing.NoticeSuspended}, "ar", "s")
}
