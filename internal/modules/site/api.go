// Package site is the platform's own public website on the platform's
// domain (ADR-0071): what افتح أونلاين is, its plans and its answers,
// server-rendered for search engines and AI assistants (robots.txt,
// sitemap.xml, llms.txt, schema.org data). It reads the plans on sale
// from billing and serves no fonts of its own (the shop's brand
// typeface). Its contact details and FAQ are edited from the super-admin
// console (S4a, ADR-0073). This file is the module's only exported
// surface (ADR-0001); see docs/modules/site.md.
package site

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Settings are the site's contact details.
	Settings = domain.Settings
	// Social is a network the site links to.
	Social = domain.Social
	// FAQ is one question, in Arabic and optionally English.
	FAQ = domain.FAQ
)

// The networks.
const (
	Facebook  = domain.Facebook
	Instagram = domain.Instagram
	TikTok    = domain.TikTok
	LinkedIn  = domain.LinkedIn
	YouTube   = domain.YouTube
	X         = domain.X
)

// Socials lists every network, in the site's order.
func Socials() []Social { return domain.Socials() }

// MaxFAQs bounds the FAQ.
const MaxFAQs = domain.MaxFAQs

// Console is what the super-admin console edits. Changes reach the
// site's pages within a minute, then the edge's cache lifetime.
type Console interface {
	Settings(ctx context.Context) (Settings, error)
	// SaveSettings checks the details and returns them as saved.
	SaveSettings(ctx context.Context, s Settings) (Settings, error)
	// FAQs are the questions in order.
	FAQs(ctx context.Context) ([]FAQ, error)
	// CreateFAQ adds a question and returns it with its ID.
	CreateFAQ(ctx context.Context, f FAQ) (FAQ, error)
	UpdateFAQ(ctx context.Context, f FAQ) (FAQ, error)
	DeleteFAQ(ctx context.Context, id ulid.ID) error
}

var _ Console = (*app.Service)(nil)

// Plans lists the plans on sale (billing.NewShowcase).
type Plans = billing.Showcase

// Font is the brand typeface the shop serves (storefront.Shop.BrandFont):
// its @font-face rules, and the files to preload. The site's stylesheet
// names its family.
type Font struct {
	Faces   string
	Preload []string
}

// DefaultWhatsApp is the platform's support number (the owner's, Egypt):
// what the site shows until it first reads the console's settings.
const DefaultWhatsApp = "+201153450716"

// DefaultCountryHeader is where the CDN puts the visitor's country
// (Cloudflare's ISO 3166-1 alpha-2 code): Egypt is priced in pounds,
// every other country in dollars.
const DefaultCountryHeader = "CF-IPCountry"
