// Package app holds the billing use cases (ADR-0045, ADR-0048): what the
// store in ctx is granted (its plan's grants while the plan applies, the
// free plan's otherwise), and the fee on each order it delivers.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// OrderStatus is what billing tells apart of an order's status.
type OrderStatus int

// Order statuses.
const (
	OrderOther OrderStatus = iota
	OrderDelivered
	OrderReturned
)

// Order is what billing needs of an order.
type Order struct {
	Status OrderStatus
	// Base is the order's goods after their discount: what a fee is on
	// (no shipping, no cash-on-delivery fee).
	Base money.Money
	// StatusAt is when the order took its status.
	StatusAt time.Time
}

// Orders reads an order of the store in ctx, in its tenant transaction
// tx; a missing order is OrderOther. cmd/platform adapts the orders
// module, which billing does not import (ADR-0046 §6).
type Orders func(ctx context.Context, tx pgx.Tx, id ulid.ID) (Order, error)

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	orders Orders
	now    func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB postgres.Beginner
	// Orders reads delivered and returned orders (the worker); nil
	// elsewhere.
	Orders Orders
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: d.DB, orders: d.Orders, now: now}
}

// Grants are what the store in ctx is granted now, in a transaction of
// its own.
func (s *Service) Grants(ctx context.Context) (g domain.Grants, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		g, txErr = s.GrantsTx(ctx, tx)
		return txErr
	})
	return g, err
}

// GrantsTx are what the store in ctx is granted now, read in its tenant
// transaction tx: two queries, three when the free plan applies.
func (s *Service) GrantsTx(ctx context.Context, tx pgx.Tx) (domain.Grants, error) {
	plan, err := s.planTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	return store.PlanGrants(ctx, tx, plan)
}

// planTx is the plan in effect for the store in ctx now: its own while
// it applies, the free plan otherwise.
func (s *Service) planTx(ctx context.Context, tx pgx.Tx) (ulid.ID, error) {
	sub, ok, err := store.Subscription(ctx, tx)
	if err != nil {
		return ulid.ID{}, err
	}
	if ok && sub.PlanApplies(s.now()) {
		return sub.PlanID, nil
	}
	free, err := store.FreePlan(ctx, tx)
	if err != nil {
		return ulid.ID{}, err
	}
	return free.ID, nil
}

// OrderMoved follows an order of the store in ctx to its current status
// (it may run more than once, and out of order: ADR-0015):
//   - delivered: the fee of the plan in effect now, once per order;
//   - returned: no fee if it was not invoiced yet, a credit on the next
//     invoice if it was (approved 2026-10-30).
func (s *Service) OrderMoved(ctx context.Context, id ulid.ID) error {
	if s.orders == nil {
		return errors.New("billing: no orders")
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		o, err := s.orders(ctx, tx, id)
		if err != nil {
			return err
		}
		switch o.Status {
		case OrderDelivered:
			return s.chargeTx(ctx, tx, id, o)
		case OrderReturned:
			f, ok, fErr := store.SellFee(ctx, tx, id)
			if fErr != nil || !ok {
				return fErr
			}
			if f, ok = f.Returned(o.StatusAt); !ok {
				return nil
			}
			return store.SetSellFeeReturned(ctx, tx, f)
		default:
			return nil
		}
	})
}

// chargeTx records a delivered order's fee, when the plan in effect
// charges one in its currency.
func (s *Service) chargeTx(ctx context.Context, tx pgx.Tx, id ulid.ID, o Order) error {
	plan, err := s.planTx(ctx, tx)
	if err != nil {
		return err
	}
	cur := string(o.Base.Currency())
	rule, ok, err := store.FeeRule(ctx, tx, plan, cur)
	if err != nil || !ok {
		return err
	}
	base := o.Base.Amount()
	_, err = store.InsertSellFee(ctx, tx, domain.SellFee{
		OrderID: id, Period: domain.PeriodOf(o.StatusAt), PlanID: plan, BaseMinor: base, RateBps: rule.RateBps,
		FeeMinor: rule.Fee(base), Currency: cur, DeliveredAt: o.StatusAt,
	})
	return err
}
