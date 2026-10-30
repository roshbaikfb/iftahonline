package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
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
