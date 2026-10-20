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

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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
}

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
		Colors: Colors{Primary: "#0f766e", Accent: "#f59e0b", Background: "#ffffff", Text: "#1f2937"},
		Font:   FontCairo,
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
