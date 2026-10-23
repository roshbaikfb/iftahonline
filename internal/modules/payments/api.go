// Package payments owns card payments through the merchant's own gateway
// account (ADR-0029): connections with sealed keys, hosted checkout,
// verified webhooks re-queried before anything changes, auto-confirm of
// paid orders, cancellation of unpaid ones, and refunds. This file is
// the module's only exported surface (ADR-0001); see
// docs/modules/payments.md.
package payments

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// CheckoutPage is where to send a card order's shopper.
	CheckoutPage = app.Checkout
	// Payment is one attempt to pay an order by card.
	Payment = domain.Payment
	// Status is a payment's status.
	Status = domain.Status
	// PaymentStatusChanged is the payments.payment.status_changed event.
	PaymentStatusChanged = domain.PaymentStatusChanged
)

// Payment statuses.
const (
	StatusPending           = domain.StatusPending
	StatusPaid              = domain.StatusPaid
	StatusFailed            = domain.StatusFailed
	StatusExpired           = domain.StatusExpired
	StatusPartiallyRefunded = domain.StatusPartiallyRefunded
	StatusRefunded          = domain.StatusRefunded
)

// EventPaymentStatusChanged is the type of PaymentStatusChanged.
const EventPaymentStatusChanged = domain.EventPaymentStatusChanged

// Checkout is what the storefront uses to take card payments. Every
// method acts on the store in ctx.
type Checkout interface {
	// CardEnabled reports whether the store has a gateway connected.
	CardEnabled(ctx context.Context) (bool, error)
	// StartCheckout returns the gateway's hosted page for a card order
	// awaiting payment (reusing a recent one); redirectURL is where the
	// shopper comes back.
	StartCheckout(ctx context.Context, orderID ulid.ID, redirectURL string) (CheckoutPage, error)
	// Refresh asks the gateway about a payment now (the shopper is back)
	// and returns it.
	Refresh(ctx context.Context, paymentID ulid.ID) (Payment, error)
}

var _ Checkout = (*app.Service)(nil)

// FakePayer plays the shopper and the fake gateway (development and
// end-to-end tests; never in production): it pays a payment on the
// fake's hosted page and returns the webhook the fake then sends, signed
// with the merchant's secret.
type FakePayer interface {
	Pay(apiKey string, paymentID ulid.ID) error
	Webhook(apiKey, webhookSecret string, paymentID ulid.ID) (body []byte, header http.Header, err error)
}

// Errors. Match with errors.Is.
var (
	ErrCardUnavailable = app.ErrCardUnavailable
	ErrNotPayable      = app.ErrNotPayable
	ErrPaymentNotFound = app.ErrPaymentNotFound
)
