// Package orders owns orders: placing them (idempotent, deduplicated,
// with stock held), their state machine, timeline, lists and exports
// (ADR-0021). This file is the module's only exported surface
// (ADR-0001); see docs/modules/orders.md.
package orders

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
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

	// StoreInfo is what orders need about the store in ctx.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
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

// Errors. Match with errors.Is.
var (
	ErrOrderNotFound        = app.ErrOrderNotFound
	ErrInvalidTransition    = domain.ErrInvalidTransition
	ErrIdempotencyKeyReused = app.ErrIdempotencyKeyReused
	ErrVersionConflict      = app.ErrVersionConflict
)
