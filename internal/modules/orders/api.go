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
	// Attribution is the campaign that brought an order (UTM, ADR-0043).
	Attribution = domain.Attribution
	// Signals are what ad providers' server APIs match a conversion
	// with (ADR-0043), kept one month.
	Signals = domain.Signals
	// Source is where an order was placed.
	Source = domain.Source
	// OutcomeReason is why an order did not reach its customer, from its
	// latest move (ADR-0065).
	OutcomeReason = domain.OutcomeReason
	// RiskAction is what the store's risk rules did to an order when it
	// was placed (ADR-0066).
	RiskAction = domain.RiskAction
	// Risk is the rules' verdict on a new storefront order.
	Risk = domain.Risk
	// RiskCheck gives that verdict; cmd/platform adapts the risk module.
	RiskCheck = app.RiskCheck
	// Placement is PlaceOrder's outcome: the order, and whether it was a
	// replay (same Idempotency-Key) or a duplicate (same phone and lines
	// within the window).
	Placement = app.Placement

	// QuoteInput asks what an order would cost before it is placed.
	QuoteInput = domain.QuoteInput
	// Quote is what it would cost: subtotal, shipping, COD fee, total.
	Quote = app.Quote

	// Coupons applies coupon codes (ADR-0042): cmd/platform adapts the
	// promotions module to it.
	Coupons = app.Coupons
	// CouponDiscount is what a coupon takes off an order.
	CouponDiscount = app.CouponDiscount
	// CouponRejected is a code that does not apply, with its reason.
	CouponRejected = app.CouponRejected

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

// Outcome reasons.
const (
	ReasonNone        = domain.ReasonNone
	ReasonRefused     = domain.ReasonRefused
	ReasonNoAnswer    = domain.ReasonNoAnswer
	ReasonUnconfirmed = domain.ReasonUnconfirmed
)

// Risk actions.
const (
	RiskNone     = domain.RiskNone
	RiskHold     = domain.RiskHold
	RiskVerify   = domain.RiskVerify
	RiskReleased = domain.RiskReleased
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
	// Track finds the order a shopper names by its code and phone (any
	// usual format), without its lines; anything else is
	// ErrOrderNotFound, whichever part was wrong.
	Track(ctx context.Context, code, phone string) (Order, error)
	// Bestsellers returns the store's products by units sold in
	// confirmed orders over the last 30 days, most first, at most limit
	// (≤ MaxBestsellers).
	Bestsellers(ctx context.Context, limit int) ([]ulid.ID, error)
}

// CodeOf is the short reference of an order shown to merchants and
// shoppers (Order.Code).
func CodeOf(id ulid.ID) string { return domain.CodeOf(id) }

// MaxBestsellers is the most products Bestsellers returns.
const MaxBestsellers = app.MaxBestsellers

var _ Shop = (*app.Service)(nil)

// Fulfilment is what a courier integration uses (ADR-0027): read an order
// to ship it, and move it as its parcel moves. Every method acts on the
// store in ctx.
type Fulfilment interface {
	// Get returns an order with its lines, or ErrOrderNotFound.
	Get(ctx context.Context, id ulid.ID) (Order, error)
	// GetTx is Get in the caller's transaction, the order locked until it
	// ends. Use it, not Get, inside a transaction: Get takes a second
	// connection from the pool.
	GetTx(ctx context.Context, tx pgx.Tx, id ulid.ID) (Order, error)
	// TransitionSystemTx moves an order in the caller's transaction on
	// behalf of actor ("carrier:bosta"), without a person's permission.
	// Moving to the current status is a no-op (moved false); a forbidden
	// move is a *TransitionError.
	TransitionSystemTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to Status, actor, note string) (moved bool, err error)
	// TransitionSystemReasonTx is TransitionSystemTx with why the order
	// did not reach its customer (ADR-0065), e.g. ReasonUnconfirmed. Only
	// a move to cancelled, failed_delivery or returned takes one.
	TransitionSystemReasonTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to Status, actor, note string, reason OutcomeReason) (moved bool, err error)
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

// Outcomes is what the risk module reads (ADR-0065): an order, and the
// store's past orders a page at a time, to learn how its customers'
// phones behave. Every method acts on the store in ctx.
type Outcomes interface {
	// Get returns an order with its lines, or ErrOrderNotFound.
	Get(ctx context.Context, id ulid.ID) (Order, error)
	// Before returns up to limit (1–100) orders with IDs below before,
	// newest first, without lines; a zero before starts at the newest.
	Before(ctx context.Context, before ulid.ID, limit int) ([]Order, error)
}

var _ Outcomes = (*app.Service)(nil)

// Errors. Match with errors.Is.
var (
	ErrOrderNotFound        = app.ErrOrderNotFound
	ErrInvalidTransition    = domain.ErrInvalidTransition
	ErrIdempotencyKeyReused = app.ErrIdempotencyKeyReused
	ErrVersionConflict      = app.ErrVersionConflict
	// ErrOrderHeld: the risk rules hold the order; it cannot be confirmed
	// until released. ErrNeedsCall: the customer's own confirmation of an
	// order the rules want verified by phone.
	ErrOrderHeld = domain.ErrOrderHeld
	ErrNeedsCall = domain.ErrNeedsCall
	ErrNotHeld   = domain.ErrNotHeld
)

// Conversions is what the server-side conversions read (ADR-0043): an
// order with its lines, and its signals; ok is false when it has none.
// It acts on the store in ctx.
type Conversions interface {
	Conversion(ctx context.Context, id ulid.ID) (o Order, s Signals, ok bool, err error)
}

var _ Conversions = (*app.Service)(nil)
