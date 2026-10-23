// Package orders owns orders: placing them (idempotent, deduplicated,
// with stock held), their state machine, timeline, lists and exports
// (ADR-0021). This file is the module's only exported surface
// (ADR-0001); see docs/modules/orders.md.
package orders

import (
	"context"

	"github.com/jackc/pgx/v5"

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
	// OrderPaymentChanged is the orders.order.payment_changed event.
	OrderPaymentChanged = domain.OrderPaymentChanged
	// PaymentMethod is how an order is paid.
	PaymentMethod = domain.PaymentMethod
	// PaymentStatus is where an order's money is.
	PaymentStatus = domain.PaymentStatus

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
	EventOrderPlaced         = domain.EventOrderPlaced
	EventOrderStatusChanged  = domain.EventOrderStatusChanged
	EventOrderPaymentChanged = domain.EventOrderPaymentChanged
)

// Payment methods and statuses.
const (
	PaymentCOD               = domain.PaymentCOD
	PaymentCard              = domain.PaymentCard
	PaymentUnpaid            = domain.PaymentUnpaid
	PaymentAwaiting          = domain.PaymentAwaiting
	PaymentPaid              = domain.PaymentPaid
	PaymentPartiallyRefunded = domain.PaymentPartiallyRefunded
	PaymentRefunded          = domain.PaymentRefunded
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

// Fulfilment is what a courier integration uses (ADR-0027): read an order
// to ship it, and move it as its parcel moves. Every method acts on the
// store in ctx.
type Fulfilment interface {
	// Get returns an order with its lines, or ErrOrderNotFound.
	Get(ctx context.Context, id ulid.ID) (Order, error)
	// TransitionSystemTx moves an order in the caller's transaction on
	// behalf of actor ("carrier:bosta"), without a person's permission.
	// Moving to the current status is a no-op (moved false); a forbidden
	// move is a *TransitionError.
	TransitionSystemTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to Status, actor, note string) (moved bool, err error)
}

var _ Fulfilment = (*app.Service)(nil)

// Payments is what a payment integration uses (ADR-0029): read an order,
// record its payment status on the gateway's word, and move it
// (confirm a paid order, cancel an unpaid one). Every method acts on the
// store in ctx.
type Payments interface {
	Fulfilment
	// SetPaymentStatusTx records a card order's payment status in the
	// caller's transaction; setting the current status (or any status of
	// a COD order) is a no-op.
	SetPaymentStatusTx(ctx context.Context, tx pgx.Tx, id ulid.ID, st PaymentStatus, actor string) (changed bool, err error)
}

var _ Payments = (*app.Service)(nil)

// Errors. Match with errors.Is.
var (
	ErrOrderNotFound        = app.ErrOrderNotFound
	ErrInvalidTransition    = domain.ErrInvalidTransition
	ErrIdempotencyKeyReused = app.ErrIdempotencyKeyReused
	ErrVersionConflict      = app.ErrVersionConflict
)
