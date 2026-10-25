// Package shipping owns delivery zones, rates, free shipping and the
// cash-on-delivery fee, and quotes what delivering an order costs
// (ADR-0025). This file is the module's only exported surface
// (ADR-0001); see docs/modules/shipping.md.
package shipping

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
)

// DTOs.
type (
	// QuoteRequest is where an order goes and what it is worth.
	QuoteRequest = domain.QuoteRequest
	// Quote is what delivering it costs.
	Quote = domain.Quote
	// DeliveryDays is how long delivery to a zone takes.
	DeliveryDays = domain.DeliveryDays
	// RatesChanged is the shipping.rates.changed event.
	RatesChanged = domain.RatesChanged

	// StoreInfo is what shipping needs about the store in ctx.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
	// CacheConfig bounds the in-memory cache of store configs.
	CacheConfig = app.CacheConfig

	// Orders is what shipments use of the orders module; cmd/platform
	// adapts orders.Fulfilment to it.
	Orders = app.Orders
	// OrderInfo is what a shipment needs of its order.
	OrderInfo = app.OrderInfo
	// ShipmentStatusChanged is the shipping.shipment.status_changed event.
	ShipmentStatusChanged = domain.ShipmentStatusChanged
)

// Errors an Orders adapter returns. Match with errors.Is.
var (
	ErrOrderUnknown     = app.ErrOrderUnknown
	ErrOrderMoveRefused = app.ErrOrderMoveRefused
)

// WebhookSecretHeader carries a carrier connection's webhook secret
// (or, for carriers that allow no headers, the "secret" query parameter).
const WebhookSecretHeader = app.SecretHeader

// Event types: rates changed on every change of a store's settings or
// zones; a shipment's status when its carrier moves it.
const (
	EventRatesChanged          = domain.EventRatesChanged
	EventShipmentStatusChanged = domain.EventShipmentStatusChanged
)

// DefaultCacheConfig is the default config cache.
func DefaultCacheConfig() CacheConfig { return app.DefaultCacheConfig() }

// FakeCourier moves parcels at the fake carrier, as a courier would: for
// development (simulating deliveries) and end-to-end tests. Statuses are
// created, in_transit, out_for_delivery, delivered, failed_attempt,
// returned and cancelled.
type FakeCourier interface {
	Advance(trackingNumber, status string) error
	// Webhook is the body the fake carrier sends for the parcel's last
	// change (the same bytes on every retry).
	Webhook(trackingNumber string) ([]byte, error)
}

// Quoter quotes deliveries for the store in ctx. Orders use it when an
// order is placed; quotes come from a cache at most CacheConfig.TTL
// stale when an invalidation is lost.
type Quoter interface {
	Quote(ctx context.Context, req QuoteRequest) (Quote, error)
}

var _ Quoter = (*app.Service)(nil)
