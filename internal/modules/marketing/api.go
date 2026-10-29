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
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
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
