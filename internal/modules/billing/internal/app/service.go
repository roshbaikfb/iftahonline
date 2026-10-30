// Package app holds the billing use cases (ADR-0045, ADR-0048): what the
// store in ctx is granted (its plan's grants while the plan applies, the
// free plan's otherwise), and the fee on each order it delivers.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
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
	db       postgres.Beginner
	orders   Orders
	stores   Stores
	jobs     jobs.Inserter
	events   eventbus.Publisher
	gateways map[string]Gateway
	sealer   Sealer
	holds    Holds
	notifier Notifier
	inbox    webhooks.Inbox
	log      *slog.Logger
	// callbackURL and dashboardURL are where the gateway sends callbacks
	// and merchants back (ADR-0053).
	callbackURL, dashboardURL string
	policy                    domain.Policy
	ids                       *ulid.Generator
	now                       func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB postgres.Beginner
	// Orders reads delivered and returned orders, Stores names stores on
	// invoices, Jobs enqueues invoice jobs (the worker); nil elsewhere.
	Orders Orders
	Stores Stores
	Jobs   jobs.Inserter
	// Events receives SubscriptionChanged; nil discards.
	Events eventbus.Publisher
	// Gateways charge cards on file by name, Sealer seals their tokens,
	// Holds closes and opens stores' storefronts, Notifier emails their
	// owners (the worker, ADR-0051); nil elsewhere.
	Gateways map[string]Gateway
	Sealer   Sealer
	Holds    Holds
	Notifier Notifier
	// Inbox gives callback jobs their receipts (the worker); Log, the
	// dropped callbacks; PublicURL and DashboardURL, the checkout's URLs.
	Inbox        webhooks.Inbox
	Log          *slog.Logger
	PublicURL    string
	DashboardURL string
	// IDs makes accounts' and invoices' IDs; nil is a new generator.
	IDs *ulid.Generator
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	if d.IDs == nil {
		d.IDs = ulid.NewGenerator()
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	svc := &Service{db: d.DB, orders: d.Orders, stores: d.Stores, jobs: d.Jobs, events: d.Events, gateways: d.Gateways, sealer: d.Sealer,
		holds: d.Holds, notifier: d.Notifier, inbox: d.Inbox, log: d.Log, dashboardURL: d.DashboardURL,
		policy: domain.DefaultPolicy(), ids: d.IDs, now: now}
	if d.PublicURL != "" {
		svc.callbackURL = CallbackURL(d.PublicURL)
	}
	return svc
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
