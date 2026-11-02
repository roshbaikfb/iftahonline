// Package site is the platform's own public website on the platform's
// domain (ADR-0071): what افتح أونلاين is, its plans and its answers,
// server-rendered for search engines and AI assistants (robots.txt,
// sitemap.xml, llms.txt, schema.org data). It reads the plans on sale
// from billing and serves no fonts of its own (the shop's brand
// typeface). This file is the module's only exported surface
// (ADR-0001); see docs/modules/site.md.
package site

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// Plans lists the plans on sale (billing.NewShowcase).
type Plans = billing.Showcase

// Font is the brand typeface the shop serves (storefront.Shop.BrandFont):
// its @font-face rules, and the files to preload. The site's stylesheet
// names its family.
type Font struct {
	Faces   string
	Preload []string
}

// DefaultWhatsApp is the platform's support number (the owner's, Egypt),
// until the console sets it (S4).
const DefaultWhatsApp = "+201153450716"

// DefaultCountryHeader is where the CDN puts the visitor's country
// (Cloudflare's ISO 3166-1 alpha-2 code): Egypt is priced in pounds,
// every other country in dollars.
const DefaultCountryHeader = "CF-IPCountry"
