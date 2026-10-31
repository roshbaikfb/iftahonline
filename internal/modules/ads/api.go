// Package ads shows ads in free stores (ADR-0062): the platform's own
// banners first, then Google AdSense, in four placements, never in the
// cart, checkout, order or thank-you pages, and never in a store that has
// the ad_free feature (entitlements decides that, not this module). This
// file and module.go are the module's only exported surface; see
// docs/modules/ads.md.
package ads

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Placement is where a page shows an ad.
	Placement = domain.Placement
	// Slot is what a placement shows: a banner, AdSense's unit, or nothing.
	Slot = domain.Slot
	// Banner is one of the platform's own ads.
	Banner = domain.Banner
	// Settings are Google AdSense's.
	Settings = domain.Settings
	// Upload is where the console's browser posts a banner's image.
	Upload = app.Upload
)

// Placements.
const (
	HomeTop       = domain.HomeTop
	Grid          = domain.Grid
	CategoryTop   = domain.CategoryTop
	ProductBottom = domain.ProductBottom
)

// Placements lists every placement.
func Placements() []Placement { return domain.Placements() }

// Shop is what the storefront asks, for free stores only. Answers are
// kept a minute in memory, shared by every store of a country.
type Shop interface {
	// Slots are what each placement shows in country.
	Slots(ctx context.Context, country string) (map[Placement]Slot, error)
	// PublisherID is AdSense's publisher when it is on, "" otherwise.
	PublisherID(ctx context.Context) (string, error)
	// AdsTxt is /ads.txt's body ("" with AdSense off).
	AdsTxt(ctx context.Context) (string, error)
	// Click counts a click on a banner on show and returns its link.
	Click(ctx context.Context, id ulid.ID) (link string, ok bool, err error)
}

// Console is what the super-admin console manages.
type Console interface {
	Settings(ctx context.Context) (Settings, error)
	SaveSettings(ctx context.Context, s Settings) error
	// Banners are every banner with its clicks of the last 30 days.
	Banners(ctx context.Context) ([]Banner, error)
	// UploadURL is a presigned upload of a banner's image.
	UploadURL(ctx context.Context, contentType string) (Upload, error)
	// CreateBanner re-encodes the uploaded image and saves the banner.
	CreateBanner(ctx context.Context, b Banner, uploadKey string) (Banner, error)
	SetActive(ctx context.Context, id ulid.ID, active bool) error
	// Thumbnail is a banner's image, signed, for the console.
	Thumbnail(b Banner) string
}

var (
	_ Shop    = (*app.Service)(nil)
	_ Console = (*app.Service)(nil)
)
