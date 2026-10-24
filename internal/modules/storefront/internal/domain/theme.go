// Package domain holds the storefront's rules: what a theme and a page
// are, which inputs are acceptable, and the defaults a new store starts
// with. It is pure: standard library and internal/platform only
// (CLAUDE.md rule 3).
package domain

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// ErrNotFound is returned by the store layer when a row is missing,
// including rows of other tenants (RLS hides them).
var ErrNotFound = errors.New("storefront: not found")

// ErrVersionConflict means the row changed since the caller read it.
var ErrVersionConflict = errs.Conflict("version_conflict", "this was changed by someone else; reload and try again")

// Font is one of the self-hosted font families (ADR-0016). Merchants
// cannot load fonts from elsewhere: the CSP allows fonts from our origin
// only.
type Font string

// Fonts.
const (
	FontCairo      Font = "cairo"
	FontTajawal    Font = "tajawal"
	FontPlexArabic Font = "ibm-plex-sans-arabic"
	FontNotoKufi   Font = "noto-kufi-arabic"
	FontSystem     Font = "system" // the device's own UI font: nothing to download
)

// minTextContrast is WCAG AA for body text.
const minTextContrast = 4.5

// Fonts lists every font.
func Fonts() []Font { return []Font{FontCairo, FontTajawal, FontPlexArabic, FontNotoKufi, FontSystem} }

// Radius is the corner rounding of buttons, cards and inputs.
type Radius string

// Radii.
const (
	RadiusNone   Radius = "none"
	RadiusSmall  Radius = "small"
	RadiusMedium Radius = "medium"
	RadiusLarge  Radius = "large"
)

// Radii lists every radius.
func Radii() []Radius { return []Radius{RadiusNone, RadiusSmall, RadiusMedium, RadiusLarge} }

// Colors are the theme's palette as #rrggbb.
type Colors struct {
	Primary    string `json:"primary"`    // buttons, links, prices
	Accent     string `json:"accent"`     // badges, highlights, countdowns
	Background string `json:"background"` // page background
	Text       string `json:"text"`       // body text
}

// ThemeSettings is what the merchant chooses. It is stored as JSON.
type ThemeSettings struct {
	Colors Colors `json:"colors"`
	Font   Font   `json:"font"`
	Radius Radius `json:"radius"`
	// Announcement is the bar above the header ("free shipping over
	// 1,000"); empty shows none.
	Announcement string `json:"announcement,omitempty"`
	// About is a few words on the store, in the footer.
	About string `json:"about,omitempty"`
	// Contact is how shoppers reach the store (footer, product page).
	Contact Contact `json:"contact"`
}

// Contact is how shoppers reach the store; empty fields are not shown.
type Contact struct {
	WhatsApp string `json:"whatsapp,omitempty"` // E.164, e.g. +201012345678
	Email    string `json:"email,omitempty"`
}

// Lengths of the theme's texts, in runes.
const (
	MaxAnnouncement = 120
	MaxAbout        = 300
)

// Theme is a store's saved settings. Version 0 means nothing is saved
// yet and Settings are the defaults.
type Theme struct {
	Settings  ThemeSettings
	Version   int
	UpdatedAt *time.Time
}

// DefaultTheme is what a store looks like before the merchant changes
// anything.
func DefaultTheme() ThemeSettings {
	return ThemeSettings{
		Colors: Colors{Primary: "#0f2e52", Accent: "#15803d", Background: "#ffffff", Text: "#0f172a"},
		Font:   FontPlexArabic,
		Radius: RadiusMedium,
	}
}

// ValidateTheme normalizes colors to lower-case #rrggbb and checks every
// field, reporting all invalid ones at once. Body text must be readable
// on the background (WCAG AA, 4.5:1).
func ValidateTheme(in ThemeSettings) (ThemeSettings, error) {
	var fields []errs.FieldError
	color := func(field, v string) string {
		v = strings.ToLower(strings.TrimSpace(v))
		if _, ok := parseHex(v); !ok {
			fields = append(fields, errs.FieldError{Field: field, Code: "invalid", Message: field + " must be a color like #1a2b3c"})
		}
		return v
	}
	out := ThemeSettings{
		Colors: Colors{
			Primary:    color("colors.primary", in.Colors.Primary),
			Accent:     color("colors.accent", in.Colors.Accent),
			Background: color("colors.background", in.Colors.Background),
			Text:       color("colors.text", in.Colors.Text),
		},
		Font:   in.Font,
		Radius: in.Radius,
	}
	line := func(field, v string, maxRunes int) string {
		clean, err := text.Clean(v, field, 0, maxRunes)
		if err != nil {
			var e *errs.Error
			if errors.As(err, &e) {
				fields = append(fields, e.Fields...)
			}
		}
		return clean
	}
	out.Announcement = line("announcement", in.Announcement, MaxAnnouncement)
	out.About = line("about", in.About, MaxAbout)
	if wa := strings.TrimSpace(in.Contact.WhatsApp); wa != "" {
		n, err := phone.Parse(wa, phone.EG) // international only (checked below): the region is not used
		if err != nil || (!strings.HasPrefix(wa, "+") && !strings.HasPrefix(wa, "00")) {
			fields = append(fields, errs.FieldError{Field: "contact.whatsapp", Code: "invalid",
				Message: "write the WhatsApp number with its country code, like +201012345678"})
		} else {
			out.Contact.WhatsApp = n.Unmasked()
		}
	}
	if em := strings.TrimSpace(in.Contact.Email); em != "" {
		n, ok := email.Normalize(em)
		if !ok {
			fields = append(fields, errs.FieldError{Field: "contact.email", Code: "invalid", Message: "contact.email must be an email address"})
		}
		out.Contact.Email = n
	}
	if !slices.Contains(Fonts(), out.Font) {
		fields = append(fields, errs.FieldError{Field: "font", Code: "invalid", Message: "font must be one of the offered fonts"})
	}
	if !slices.Contains(Radii(), out.Radius) {
		fields = append(fields, errs.FieldError{Field: "radius", Code: "invalid", Message: "radius must be none, small, medium or large"})
	}
	if len(fields) == 0 && ContrastRatio(out.Colors.Text, out.Colors.Background) < minTextContrast {
		fields = append(fields, errs.FieldError{Field: "colors.text", Code: "low_contrast",
			Message: "text must contrast with the background at least 4.5:1 to be readable"})
	}
	if len(fields) > 0 {
		return ThemeSettings{}, errs.Validation(fields...)
	}
	return out, nil
}

// parseHex parses #rrggbb into 0..1 channels.
func parseHex(s string) ([3]float64, bool) {
	var c [3]float64
	if len(s) != 7 || s[0] != '#' {
		return c, false
	}
	for i := range 3 {
		n, err := strconv.ParseUint(s[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return c, false
		}
		c[i] = float64(n) / 255
	}
	return c, true
}

// luminance is the WCAG relative luminance of a parsed color.
func luminance(c [3]float64) float64 {
	var l [3]float64
	for i, v := range c {
		if v <= 0.03928 {
			l[i] = v / 12.92
		} else {
			l[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
}

// ContrastRatio is the WCAG contrast ratio of two #rrggbb colors, from 1
// to 21. An invalid color counts as no contrast (1).
func ContrastRatio(a, b string) float64 {
	ca, okA := parseHex(a)
	cb, okB := parseHex(b)
	if !okA || !okB {
		return 1
	}
	la, lb := luminance(ca), luminance(cb)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// OnColor returns the text color (white or near-black) that reads best on
// bg, for buttons and badges drawn in the merchant's colors.
func OnColor(bg string) string {
	const light, dark = "#ffffff", "#111111"
	if ContrastRatio(light, bg) >= ContrastRatio(dark, bg) {
		return light
	}
	return dark
}
