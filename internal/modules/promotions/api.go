// Package promotions owns the store's coupons (ADR-0042): the merchant's
// codes, what each takes off an order, and its uses. Orders take a
// coupon through Coupons, in the transaction that places them; an order
// cancelled before it ships gives its use back. This file is the
// module's only exported surface (ADR-0001); see
// docs/modules/promotions.md.
package promotions

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Discount is what a coupon takes off an order.
	Discount = domain.Discount
	// Rejected is a coupon that does not apply, with its Reason.
	Rejected = domain.Rejected
	// Reason is why a coupon does not apply.
	Reason = domain.Reason
)

// Reasons.
const (
	NotFound    = domain.NotFound
	Inactive    = domain.Inactive
	NotStarted  = domain.NotStarted
	Expired     = domain.Expired
	MinSubtotal = domain.MinSubtotal
	UsedUp      = domain.UsedUp
	PerCustomer = domain.PerCustomer
)

// Coupons is what orders use (through a port cmd/platform adapts). Every
// method acts on the store in ctx; a code that does not apply is a
// *Rejected (match with errors.As).
type Coupons interface {
	// Preview is what a code would take off items costing subtotal, for
	// a quote (the per-customer limit is checked when the order is
	// placed).
	Preview(ctx context.Context, code string, subtotal money.Money) (Discount, error)
	// RedeemTx takes the code's use for an order of customer, in the
	// order's transaction, with every limit checked under the coupon's
	// lock.
	RedeemTx(ctx context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (Discount, error)
}

var _ Coupons = (*app.Service)(nil)

// ReleaseCouponsSubscriber is the event bus subscriber name of
// Module.OrderStatusHandler: subscribe it to
// orders.EventOrderStatusChanged on every role; only the worker's
// handles.
const ReleaseCouponsSubscriber = "promotions.release_coupons"
