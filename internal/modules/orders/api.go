// Package orders owns orders: placing them (idempotent, deduplicated,
// with stock held), their state machine, timeline, lists and exports
// (ADR-0021). This file is the module's only exported surface
// (ADR-0001); see docs/modules/orders.md.
package orders

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Order is a placed order.
	Order = domain.Order
	// Status is an order's state.
	Status = domain.Status
	// TransitionError is a move the state machine forbids; it matches
	// ErrInvalidTransition.
	TransitionError = domain.TransitionError

	// OrderPlaced is the orders.order.placed event.
	OrderPlaced = domain.OrderPlaced
	// OrderStatusChanged is the orders.order.status_changed event.
	OrderStatusChanged = domain.OrderStatusChanged

	// NewOrder is an order as a shopper or staff member submits it.
	NewOrder = domain.NewOrder
	// LineInput is one variant and quantity of a NewOrder.
	LineInput = domain.LineInput
	// Source is where an order was placed.
	Source = domain.Source
	// Placement is PlaceOrder's outcome: the order, and whether it was a
	// replay (same Idempotency-Key) or a duplicate (same phone and lines
	// within the window).
	Placement = app.Placement

	// QuoteInput asks what an order would cost before it is placed.
	QuoteInput = domain.QuoteInput
	// Quote is what it would cost: subtotal, shipping, COD fee, total.
	Quote = app.Quote

	// StoreInfo is what orders need about the store in ctx.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
)

// Sources.
const (
	SourceStorefront = domain.SourceStorefront
	SourceMerchant   = domain.SourceMerchant
)

// Event types.
const (
	EventOrderPlaced        = domain.EventOrderPlaced
	EventOrderStatusChanged = domain.EventOrderStatusChanged
)

// Statuses.
const (
	StatusPending        = domain.StatusPending
	StatusConfirmed      = domain.StatusConfirmed
	StatusReadyToShip    = domain.StatusReadyToShip
	StatusShipped        = domain.StatusShipped
	StatusDelivered      = domain.StatusDelivered
	StatusCancelled      = domain.StatusCancelled
	StatusFailedDelivery = domain.StatusFailedDelivery
	StatusReturned       = domain.StatusReturned
)

// Shop is what the storefront uses: shoppers place orders and see the
// thank-you page. Every method acts on the store in ctx.
type Shop interface {
	// PlaceOrder places an order (ADR-0021). Errors to expect: a
	// validation error (errs.KindValidation, with fields),
	// customers.ErrCustomerBlocked, catalog.ErrInsufficientStock and
	// ErrIdempotencyKeyReused.
	PlaceOrder(ctx context.Context, in NewOrder) (Placement, error)
	// Get returns an order with its lines, or ErrOrderNotFound.
	Get(ctx context.Context, id ulid.ID) (Order, error)
	// Quote prices an order the way PlaceOrder will, for showing the
	// shopper the shipping and COD fee before they submit. Errors: a
	// validation error, as PlaceOrder's.
	Quote(ctx context.Context, in QuoteInput) (Quote, error)
}

var _ Shop = (*app.Service)(nil)

// Errors. Match with errors.Is.
var (
	ErrOrderNotFound        = app.ErrOrderNotFound
	ErrInvalidTransition    = domain.ErrInvalidTransition
	ErrIdempotencyKeyReused = app.ErrIdempotencyKeyReused
	ErrVersionConflict      = app.ErrVersionConflict
)
