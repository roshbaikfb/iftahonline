// Package domain holds the rules of a store's ad pixels (ADR-0043):
// which ad platforms a store can measure with, what each one's IDs look
// like, which origins its browser script needs, and the shopper's
// consent.
package domain

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// ErrNotFound is returned by the store layer when a row is missing,
// which includes rows of other tenants (RLS hides them).
var ErrNotFound = errors.New("marketing: not found")

// Provider is an ad or analytics platform a store can connect.
type Provider string

// Providers. The list is closed: a merchant picks one of these and types
// its IDs; no merchant script ever reaches a page (ADR-0043).
const (
	Meta      Provider = "meta"
	TikTok    Provider = "tiktok"
	Snap      Provider = "snap"
	GoogleAds Provider = "google_ads"
	GA4       Provider = "ga4"
)

// Spec is what we know about a provider: how its IDs look, whether it
// has a server API we send conversions to, and the origins its browser
// script loads from and sends to (the page's CSP allows exactly these,
// and only while the store has the provider on).
type Spec struct {
	Provider Provider
	// ID is the pattern of the pixel (or measurement) ID.
	ID *regexp.Regexp
	// Label is the pattern of the conversion label, for providers that
	// need one (Google Ads); nil if none.
	Label *regexp.Regexp
	// TestCode says the provider takes a test event code (its events
	// then show in its test tool, not its reports).
	TestCode bool
	// Server says we send its conversions from the server too (its
	// Conversions API), which needs a token.
	Server bool
	// Script, Connect and Frame are the CSP origins of its browser tag.
	Script, Connect, Frame []string
}

var (
	digits     = regexp.MustCompile(`^[0-9]{10,20}$`)
	tiktokID   = regexp.MustCompile(`^[A-Z0-9]{15,25}$`)
	snapID     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	adsID      = regexp.MustCompile(`^AW-[0-9]{6,15}$`)
	adsLabel   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,40}$`)
	ga4ID      = regexp.MustCompile(`^G-[A-Z0-9]{4,15}$`)
	testCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

// gtag gives a spec Google's tag's origins, shared by Google Ads and GA4.
func gtag(s Spec) Spec {
	s.Script = []string{"https://www.googletagmanager.com"}
	s.Connect = []string{"https://www.google-analytics.com", "https://*.google-analytics.com", "https://*.analytics.google.com",
		"https://www.googletagmanager.com", "https://www.google.com", "https://googleads.g.doubleclick.net"}
	s.Frame = []string{"https://td.doubleclick.net", "https://www.googletagmanager.com"}
	return s
}

// specs are the providers, in the order the dashboard lists them.
var specs = []Spec{ //nolint:gochecknoglobals // constant registry
	{Provider: Meta, ID: digits, TestCode: true, Server: true,
		Script: []string{"https://connect.facebook.net"}, Connect: []string{"https://www.facebook.com", "https://connect.facebook.net"}},
	{Provider: TikTok, ID: tiktokID, TestCode: true, Server: true,
		Script: []string{"https://analytics.tiktok.com"}, Connect: []string{"https://analytics.tiktok.com"}},
	{Provider: Snap, ID: snapID, Server: true,
		Script: []string{"https://sc-static.net"}, Connect: []string{"https://tr.snapchat.com", "https://tr-shadow.snapchat.com"}},
	gtag(Spec{Provider: GoogleAds, ID: adsID, Label: adsLabel}),
	gtag(Spec{Provider: GA4, ID: ga4ID, Server: true}),
}

// Specs lists every provider.
func Specs() []Spec { return slices.Clone(specs) }

// SpecOf returns a provider's spec; ok is false for an unknown name.
func SpecOf(p Provider) (Spec, bool) {
	i := slices.IndexFunc(specs, func(s Spec) bool { return s.Provider == p })
	if i < 0 {
		return Spec{}, false
	}
	return specs[i], true
}

// Pixel is a store's connection to one provider.
type Pixel struct {
	Provider Provider
	PixelID  string
	Label    string
	TestCode string
	Enabled  bool
	// HasServerToken says a Conversions API token is saved. The token
	// itself never leaves the server.
	HasServerToken bool
	Version        int
	UpdatedAt      time.Time
}

// Token bounds: the providers' tokens are long opaque strings.
const (
	MinTokenLen = 8
	MaxTokenLen = 1024
)

// PixelInput is what the merchant saves for a provider.
type PixelInput struct {
	PixelID  string
	Label    string
	TestCode string
	Enabled  bool
	// ServerToken is the Conversions API token: nil keeps the saved one,
	// "" removes it.
	ServerToken *string
	// Version is the version read (optimistic locking); 0 when the store
	// has none yet.
	Version int
}

// Normalize trims what the merchant pasted.
func (in PixelInput) Normalize() PixelInput {
	in.PixelID = strings.TrimSpace(in.PixelID)
	in.Label = strings.TrimSpace(in.Label)
	in.TestCode = strings.TrimSpace(in.TestCode)
	if in.ServerToken != nil {
		t := strings.TrimSpace(*in.ServerToken)
		in.ServerToken = &t
	}
	return in
}

// Validate checks an input against the provider's spec.
func (in PixelInput) Validate(s Spec) error {
	var fields []errs.FieldError
	bad := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}
	if !s.ID.MatchString(in.PixelID) {
		bad("pixel_id", "invalid", "not an ID of "+string(s.Provider))
	}
	switch {
	case s.Label != nil && !s.Label.MatchString(in.Label):
		bad("label", "invalid", "the purchase conversion's label")
	case s.Label == nil && in.Label != "":
		bad("label", "unexpected", string(s.Provider)+" takes no label")
	}
	switch {
	case in.TestCode == "":
	case !s.TestCode:
		bad("test_code", "unexpected", string(s.Provider)+" takes no test code")
	case !testCodeRe.MatchString(in.TestCode):
		bad("test_code", "invalid", "1 to 40 letters, digits, - or _")
	}
	if t := in.ServerToken; t != nil && *t != "" {
		switch {
		case !s.Server:
			bad("server_token", "unexpected", string(s.Provider)+" has no server API here")
		case len(*t) < MinTokenLen || len(*t) > MaxTokenLen || !printable(*t):
			bad("server_token", "invalid", "the token as the provider shows it")
		}
	}
	if in.Version < 0 {
		bad("version", "range", "0 or more")
	}
	if len(fields) > 0 {
		return errs.Validation(fields...)
	}
	return nil
}

// printable is visible ASCII with no spaces: tokens are pasted, and a
// stray newline or quote would only fail later at the provider.
func printable(s string) bool {
	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// Consent is when a store's pages may load its pixels.
type Consent string

// Consent modes.
const (
	// ConsentAuto asks the shopper where the store's country requires it
	// (Saudi Arabia's PDPL) and loads the pixels otherwise. The
	// storefront knows the country; this module does not need to.
	ConsentAuto Consent = "auto"
	// ConsentRequired always asks first.
	ConsentRequired Consent = "required"
	// ConsentOff never asks.
	ConsentOff Consent = "off"
)

// Valid reports whether c is a mode.
func (c Consent) Valid() bool { return c == ConsentAuto || c == ConsentRequired || c == ConsentOff }

// Settings are a store's marketing settings.
type Settings struct {
	Consent Consent
	Version int
}

// DefaultSettings are a store's settings before it saves any.
func DefaultSettings() Settings { return Settings{Consent: ConsentAuto} }

// BrowserPixel is what a page needs to load one provider's tag.
type BrowserPixel struct {
	Provider Provider
	PixelID  string
	Label    string
}

// Browser is what the storefront renders: the enabled providers' public
// IDs, the consent mode, and the CSP origins those providers need.
type Browser struct {
	Pixels  []BrowserPixel
	Consent Consent
	Script  []string
	Connect []string
	Frame   []string
}

// BrowserOf builds what pages render from the store's pixels, in the
// registry's order, each origin once.
func BrowserOf(pixels []Pixel, s Settings) Browser {
	b := Browser{Consent: s.Consent}
	for _, spec := range specs {
		i := slices.IndexFunc(pixels, func(p Pixel) bool { return p.Provider == spec.Provider && p.Enabled })
		if i < 0 {
			continue
		}
		p := pixels[i]
		b.Pixels = append(b.Pixels, BrowserPixel{Provider: p.Provider, PixelID: p.PixelID, Label: p.Label})
		b.Script = appendNew(b.Script, spec.Script)
		b.Connect = appendNew(b.Connect, spec.Connect)
		b.Frame = appendNew(b.Frame, spec.Frame)
	}
	return b
}

func appendNew(dst, src []string) []string {
	for _, s := range src {
		if !slices.Contains(dst, s) {
			dst = append(dst, s)
		}
	}
	return dst
}
