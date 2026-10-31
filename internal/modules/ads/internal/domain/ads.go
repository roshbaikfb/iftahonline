// Package domain holds the rules of ads in free stores (ADR-0062): the
// placements and their sizes, the platform's banners and Google AdSense's
// settings, and which one a placement shows. It imports only the
// standard library and internal/platform (CLAUDE.md rule 3).
package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Placement is where a page shows an ad (approved 2026-10-31: these four,
// at most two a page, never in the cart, checkout, order or thank-you
// pages).
type Placement string

// Placements.
const (
	HomeTop       Placement = "home_top"       // under the home page's first section
	Grid          Placement = "grid"           // between products: home and category pages
	CategoryTop   Placement = "category_top"   // above a category's products
	ProductBottom Placement = "product_bottom" // under a product's description
)

// Placements are every placement, in the console's order.
func Placements() []Placement { return []Placement{HomeTop, Grid, CategoryTop, ProductBottom} }

// Valid reports whether p is a placement.
func (p Placement) Valid() bool { return slices.Contains(Placements(), p) }

// Size is a placement's box, reserved before its ad loads so nothing
// moves (CLS): a banner strip or a medium rectangle.
func (p Placement) Size() (width, height int) {
	if p == HomeTop || p == CategoryTop {
		return 320, 100
	}
	return 300, 250
}

// Settings are Google AdSense's: off, or a publisher and a unit per
// placement (a placement without one shows only banners).
type Settings struct {
	AdSenseEnabled bool
	PublisherID    string               // ca-pub-0000000000000000
	Units          map[Placement]string // 10 digits
}

var (
	publisherRE = regexp.MustCompile(`^ca-pub-[0-9]{16}$`)
	unitRE      = regexp.MustCompile(`^[0-9]{10}$`)
)

// Validate checks settings from the console.
func (s Settings) Validate() error {
	var fe []errs.FieldError
	if s.PublisherID != "" && !publisherRE.MatchString(s.PublisherID) {
		fe = append(fe, errs.FieldError{Field: "publisher_id", Code: "invalid", Message: "ca-pub- and 16 digits"})
	}
	if s.AdSenseEnabled && s.PublisherID == "" {
		fe = append(fe, errs.FieldError{Field: "publisher_id", Code: "required", Message: "AdSense needs a publisher ID"})
	}
	for p, u := range s.Units {
		if !p.Valid() || (u != "" && !unitRE.MatchString(u)) {
			fe = append(fe, errs.FieldError{Field: "units." + string(p), Code: "invalid", Message: "a placement and a 10-digit unit"})
		}
	}
	if len(fe) > 0 {
		slices.SortFunc(fe, func(a, b errs.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return errs.Validation(fe...)
	}
	return nil
}

// AdsTxt is /ads.txt's line authorizing the publisher (IAB ads.txt), or
// "" when AdSense is off.
func (s Settings) AdsTxt() string {
	if !s.AdSenseEnabled || s.PublisherID == "" {
		return ""
	}
	return "google.com, " + strings.TrimPrefix(s.PublisherID, "ca-") + ", DIRECT, f08c47fec0942fa0\n"
}

// Banner is one of the platform's own ads.
type Banner struct {
	ID            ulid.ID
	Title         string // for the console and screen readers
	ImageKey      string
	Width, Height int
	Link          string // https
	Placement     Placement
	Country       string // "" for every country
	StartsAt      time.Time
	EndsAt        time.Time // zero: no end
	Weight        int       // 1–100: its share among its placement's banners
	Active        bool
	Clicks        int64 // the console's: the last 30 days
}

// MaxImageBytes bounds a banner's upload.
const MaxImageBytes = 2 << 20

// Validate checks a new banner from the console (its image is checked
// when it is ingested).
func (b Banner) Validate() error {
	var fe []errs.FieldError
	if n := utf8.RuneCountInString(strings.TrimSpace(b.Title)); n < 1 || n > 120 {
		fe = append(fe, errs.FieldError{Field: "title", Code: "invalid", Message: "1 to 120 characters"})
	}
	if u, err := url.Parse(b.Link); err != nil || u.Scheme != "https" || u.Host == "" || len(b.Link) > 500 {
		fe = append(fe, errs.FieldError{Field: "link_url", Code: "invalid", Message: "an https link"})
	}
	if !b.Placement.Valid() {
		fe = append(fe, errs.FieldError{Field: "placement", Code: "invalid", Message: "home_top, grid, category_top or product_bottom"})
	}
	if b.Country != "" && !slices.Contains([]string{"EG", "SA", "AE"}, b.Country) {
		fe = append(fe, errs.FieldError{Field: "country", Code: "invalid", Message: "EG, SA or AE"})
	}
	if b.StartsAt.IsZero() || (!b.EndsAt.IsZero() && !b.EndsAt.After(b.StartsAt)) {
		fe = append(fe, errs.FieldError{Field: "ends_at", Code: "invalid", Message: "after the start"})
	}
	if b.Weight < 1 || b.Weight > 100 {
		fe = append(fe, errs.FieldError{Field: "weight", Code: "invalid", Message: "1 to 100"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// ImageKey is where a banner's re-encoded image is kept: the platform's
// space in the media bucket, never a store's.
func ImageKey(id ulid.ID, format string) string {
	return fmt.Sprintf("platform/ads/%s.%s", id, format)
}

// Slot is what a placement shows: a banner, or AdSense's unit, or nothing.
type Slot struct {
	Placement Placement
	Banner    *Banner
	Unit      string // AdSense's, when no banner shows
}

// Empty reports whether the slot shows nothing (its space is not kept).
func (s Slot) Empty() bool { return s.Banner == nil && s.Unit == "" }

// Choose fills each placement (approved: the platform's banners first,
// then AdSense where it has a unit): a banner by weight among the live
// ones, drawn with pick(n) in [0, n).
func Choose(s Settings, live []Banner, pick func(n int) int) map[Placement]Slot {
	out := make(map[Placement]Slot, len(Placements()))
	for _, p := range Placements() {
		slot := Slot{Placement: p}
		var pool []Banner
		total := 0
		for _, b := range live {
			if b.Placement == p {
				pool = append(pool, b)
				total += b.Weight
			}
		}
		if total > 0 {
			n := pick(total)
			for i := range pool {
				if n < pool[i].Weight {
					slot.Banner = &pool[i]
					break
				}
				n -= pool[i].Weight
			}
		} else if s.AdSenseEnabled && s.Units[p] != "" {
			slot.Unit = s.Units[p]
		}
		out[p] = slot
	}
	return out
}
