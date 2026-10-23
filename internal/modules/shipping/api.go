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
	// RatesChanged is the shipping.rates.changed event.
	RatesChanged = domain.RatesChanged

	// StoreInfo is what shipping needs about the store in ctx.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
	// CacheConfig bounds the in-memory cache of store configs.
	CacheConfig = app.CacheConfig
)

// EventRatesChanged is published on every change of a store's settings
// or zones.
const EventRatesChanged = domain.EventRatesChanged

// DefaultCacheConfig is the default config cache.
func DefaultCacheConfig() CacheConfig { return app.DefaultCacheConfig() }

// Quoter quotes deliveries for the store in ctx. Orders use it when an
// order is placed; quotes come from a cache at most CacheConfig.TTL
// stale when an invalidation is lost.
type Quoter interface {
	Quote(ctx context.Context, req QuoteRequest) (Quote, error)
}

var _ Quoter = (*app.Service)(nil)
