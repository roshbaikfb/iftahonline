// Package billing owns plans, what they grant, and each store's
// subscription (ADR-0045). It is the only place that knows plans by
// name: other modules ask the entitlements module what a store may do,
// never which plan it is on (internal/tools/noplannames).
// This file is the module's only exported surface (ADR-0001); see
// docs/modules/billing.md.
package billing

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Grant is what a plan gives of one feature. Unlimited, or a Value:
	// 0 or 1 for a flag, a count, or units per calendar month.
	Grant = domain.Grant
	// Grants are by feature key; a feature not listed is not granted.
	Grants = domain.Grants
	// Order is what fees need of an order (cmd/platform adapts orders).
	Order = app.Order
	// OrderStatus tells delivered and returned orders from the rest.
	OrderStatus = app.OrderStatus
	// Orders reads an order in the store's tenant transaction; a missing
	// one is OrderOther.
	Orders = app.Orders
	// StoreInfo is what invoices need of a store: its name and currency.
	StoreInfo = app.StoreInfo
	// Stores reads the store in ctx (cmd/platform adapts tenancy).
	Stores = app.Stores
)

// Order statuses.
const (
	OrderOther     = app.OrderOther
	OrderDelivered = app.OrderDelivered
	OrderReturned  = app.OrderReturned
)

// SellFeesSubscriber is the event bus subscriber name of Fees.OrderMoved,
// subscribed to orders.order.status_changed (the worker role).
const SellFeesSubscriber = "billing.sell_fees"

// Fees records pay-as-you-sell fees (ADR-0048).
type Fees interface {
	// OrderMoved follows an order of the store in ctx to its current
	// status: a delivered order's fee (once), a returned order's fee
	// cancelled or credited. Safe to repeat, in any order.
	OrderMoved(ctx context.Context, order ulid.ID) error
}

// GrantSource is what the entitlements module reads.
type GrantSource interface {
	// GrantsTx are what the store in ctx is granted now, read in its
	// tenant transaction tx: its plan's while the plan applies (trial
	// running, paid, or past due in grace), the free plan's otherwise.
	GrantsTx(ctx context.Context, tx pgx.Tx) (Grants, error)
}

var (
	_ GrantSource = (*app.Service)(nil)
	_ Fees        = (*app.Service)(nil)
)
