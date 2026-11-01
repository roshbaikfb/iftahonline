// Package paytest holds the payments module's test stand-ins.
package paytest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Orders keeps orders in memory and records what payments do to them;
// it implements the payments' Orders port.
type Orders struct {
	mu     sync.Mutex
	ids    *ulid.Generator
	orders map[ulid.ID]*orders.Order
	log    []string
}

// NewOrders returns no orders.
func NewOrders() *Orders {
	return &Orders{ids: ulid.NewGenerator(), orders: map[ulid.ID]*orders.Order{}}
}

// Add places a pending 520 EGP order paid by method, placed age ago.
func (o *Orders) Add(t testing.TB, method orders.PaymentMethod, age time.Duration) ulid.ID {
	t.Helper()
	total, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	p, err := phone.Parse("01012345678", phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	st := orders.PaymentAwaiting
	if method == orders.PaymentCOD {
		st = orders.PaymentUnpaid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	id := o.ids.New()
	ord := &orders.Order{ID: id, Status: orders.StatusPending, PaymentMethod: method, PaymentStatus: st, Total: total,
		CreatedAt: time.Now().Add(-age).UTC()}
	ord.Contact.Name, ord.Contact.Phone = "منى أحمد", p
	o.orders[id] = ord
	return id
}

// AddDeposit places a pending 520 EGP cash order whose 65 EGP delivery
// awaits its prepayment (ADR-0067).
func (o *Orders) AddDeposit(t testing.TB) ulid.ID {
	t.Helper()
	deposit, err := money.New(6500, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	id := o.Add(t, orders.PaymentCOD, 0)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orders[id].RiskAction, o.orders[id].Deposit, o.orders[id].DepositStatus = orders.RiskPrepay, deposit, orders.PaymentAwaiting
	return id
}

// Get implements the port.
func (o *Orders) Get(_ context.Context, id ulid.ID) (orders.Order, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	got, ok := o.orders[id]
	if !ok {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	return *got, nil
}

// GetTx implements the port.
func (o *Orders) GetTx(ctx context.Context, _ pgx.Tx, id ulid.ID) (orders.Order, error) {
	return o.Get(ctx, id)
}

// TransitionSystemTx implements the port.
func (o *Orders) TransitionSystemTx(_ context.Context, _ pgx.Tx, id ulid.ID, to orders.Status, actor, _ string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	got, ok := o.orders[id]
	if !ok {
		return false, orders.ErrOrderNotFound
	}
	if got.Status == to {
		return false, nil
	}
	got.Status = to
	o.log = append(o.log, string(to)+" by "+actor)
	return true, nil
}

// SetPaymentStatusTx implements the port.
func (o *Orders) SetPaymentStatusTx(_ context.Context, _ pgx.Tx, id ulid.ID, st orders.PaymentStatus, actor string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	got, ok := o.orders[id]
	if !ok {
		return false, orders.ErrOrderNotFound
	}
	if got.PaymentStatus == st {
		return false, nil
	}
	got.PaymentStatus = st
	o.log = append(o.log, "payment "+string(st)+" by "+actor)
	return true, nil
}

// Order returns an order as it is now.
func (o *Orders) Order(id ulid.ID) orders.Order {
	o.mu.Lock()
	defer o.mu.Unlock()
	return *o.orders[id]
}

// SetStatus moves an order as staff would.
func (o *Orders) SetStatus(id ulid.ID, st orders.Status) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orders[id].Status = st
}

// History lists what payments did to the orders, in order.
func (o *Orders) History() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.log...)
}
