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
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/payapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
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

// WatchOrdersSubscriber is the event bus subscriber name of
// Module.OrderPlacedHandler: subscribe it to orders.EventOrderPlaced on
// every role's bus, so each card order's expiry is scheduled with the
// order (ADR-0031).
const WatchOrdersSubscriber = "payments.watch_card_orders"

// EventPaymentStatusChanged is the type of PaymentStatusChanged.
const EventPaymentStatusChanged = domain.EventPaymentStatusChanged

// EventConnectionChanged is published when a store connects or
// disconnects a gateway (ConnectionChanged): whether it takes cards
// changed.
const EventConnectionChanged = domain.EventConnectionChanged

// ConnectionChanged is the payments.connection.changed event.
type ConnectionChanged = domain.ConnectionChanged

// SettingsChanged is the payments.settings.changed event.
type SettingsChanged = domain.SettingsChanged

// EventSettingsChanged is published when the method checkout starts on
// changes.
const EventSettingsChanged = domain.EventSettingsChanged

// Checkout is what the storefront uses to take card payments. Every
// method acts on the store in ctx.
type Checkout interface {
	// CardEnabled reports whether the store has a gateway connected.
	CardEnabled(ctx context.Context) (bool, error)
	// StartCheckout returns the gateway's hosted page for a card order
	// awaiting payment (reusing a recent one); redirectURL is where the
	// shopper comes back. A prepaid delivery is ErrNotPayable here.
	StartCheckout(ctx context.Context, orderID ulid.ID, redirectURL string) (CheckoutPage, error)
	// Refresh asks the gateway about a payment now (the shopper is back)
	// and returns it.
	Refresh(ctx context.Context, paymentID ulid.ID) (Payment, error)
}

var _ Checkout = (*app.Service)(nil)

// CardCheck tells the storefront whether the store in ctx takes cards.
// It reads no keys, so the storefront role (which holds none) can use
// it.
type CardCheck interface {
	CardEnabled(ctx context.Context) (bool, error)
	// CardDefault reports whether checkout starts on card (ADR-0036).
	CardDefault(ctx context.Context) (bool, error)
}

var _ CardCheck = (*app.Service)(nil)

// NewCardCheck builds a CardCheck on pool; plan says whether the store's
// plan includes card payments (nil includes them: tests).
func NewCardCheck(pool *pgxpool.Pool, plan entitlements.Entitlements) CardCheck {
	return app.NewService(app.Deps{DB: pool, Plan: plan})
}

// StartURL is where the storefront sends a card order's shopper: the
// API host's checkout route (apiOrigin, e.g. https://app.iftahonline.com),
// which opens the gateway's page and brings the shopper back to back,
// a URL on one of the store's hosts (ADR-0030).
func StartURL(apiOrigin string, store tenant.StoreID, orderID ulid.ID, back string) string {
	return strings.TrimRight(apiOrigin, "/") + "/v1/pay/" + store.String() + "/" + orderID.String() + "?return=" + url.QueryEscape(back)
}

// DepositURL is StartURL for a cash order's prepaid delivery, from the
// link sent to the customer's phone: token is that link's (ADR-0070).
func DepositURL(apiOrigin string, store tenant.StoreID, orderID ulid.ID, back, token string) string {
	return StartURL(apiOrigin, store, orderID, back) + "&t=" + url.QueryEscape(token)
}

// DepositLinkCheck reports whether a token is the link sent to the phone
// of an order's customer, in the store in ctx (messaging's).
type DepositLinkCheck = payapi.LinkCheck

// HostOwner reports which store a host belongs to (tenancy's resolver).
type HostOwner = payapi.HostOwner

// ClientIPFunc is the server's real-IP lookup.
type ClientIPFunc = payapi.ClientIPFunc

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
