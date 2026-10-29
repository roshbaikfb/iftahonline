// Package marketing owns a store's ad pixels and the shopper's consent
// (ADR-0043): which ad and analytics providers its pages measure with,
// their public IDs, the sealed Conversions API tokens, and when pages
// may load the pixels. The storefront renders them through Pixels. This
// file is the module's only exported surface (ADR-0001); see
// docs/modules/marketing.md.
package marketing

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Provider is an ad or analytics provider.
	Provider = domain.Provider
	// Consent is when a store's pages may load its pixels.
	Consent = domain.Consent
	// Browser is what a store's pages render: its enabled pixels' public
	// IDs, its consent mode and the CSP origins they need.
	Browser = domain.Browser
	// BrowserPixel is one provider's tag on a page.
	BrowserPixel = domain.BrowserPixel
	// PixelsChanged is the marketing.pixels.changed event.
	PixelsChanged = domain.PixelsChanged
	// Feed is a product feed's format (ADR-0044).
	Feed = domain.Feed
	// FeedChanged is the marketing.feed.changed event.
	FeedChanged = domain.FeedChanged
	// Conversion is a conversion the server reports (ADR-0043), with the
	// event_id its page reported.
	Conversion = app.Conversion
	// User is who a conversion matches: normalized and hashed before it
	// leaves; sealed while it waits.
	User = capi.User
)

// Conversion names.
const (
	AddToCart        = capi.AddToCart
	InitiateCheckout = capi.InitiateCheckout
	Purchase         = capi.Purchase
)

// Providers.
const (
	Meta      = domain.Meta
	TikTok    = domain.TikTok
	Snap      = domain.Snap
	GoogleAds = domain.GoogleAds
	GA4       = domain.GA4
)

// Consent modes.
const (
	ConsentAuto     = domain.ConsentAuto
	ConsentRequired = domain.ConsentRequired
	ConsentOff      = domain.ConsentOff
)

// EventPixelsChanged is published when a store saves or removes a pixel
// or changes its consent mode: what its pages load changed.
const EventPixelsChanged = domain.EventPixelsChanged

// Pixels is what the storefront uses. It acts on the store in ctx and
// never reads a token.
type Pixels interface {
	Browser(ctx context.Context) (Browser, error)
}

var _ Pixels = (*app.Service)(nil)

// Conversions is what the storefront reports server-side conversions
// through: one job per provider the store sends them to, nothing for a
// store without any. It acts on the store in ctx.
type Conversions interface {
	Track(ctx context.Context, c Conversion) error
}

var _ Conversions = (*app.Service)(nil)

// Product feeds (ADR-0044).
const (
	FeedGoogle = domain.FeedGoogle
	FeedMeta   = domain.FeedMeta
	// EventFeedChanged is published when a store turns its feeds on or
	// off or replaces their key: the edge's copies must go.
	EventFeedChanged = domain.EventFeedChanged
)

// FeedPath is where a store's storefront serves feed f with key.
func FeedPath(f Feed, key string) string { return domain.FeedPath(f, key) }

// Feeds is what the storefront asks before it serves a product feed. It
// acts on the store in ctx.
type Feeds interface {
	// FeedAllowed reports whether key opens the store's feeds.
	FeedAllowed(ctx context.Context, key string) (bool, error)
}

var _ Feeds = (*app.Service)(nil)

// PurchaseEventID is the event_id of an order's Purchase, on its
// thank-you page and in the server's event.
func PurchaseEventID(order ulid.ID) string { return app.PurchaseEventID(order) }

// PurchasesSubscriber is the event bus subscriber name of
// Module.OrderPlacedHandler: subscribe it to orders.EventOrderPlaced on
// every role; only the worker's handles.
const PurchasesSubscriber = "marketing.report_purchases"
