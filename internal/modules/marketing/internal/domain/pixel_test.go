package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func ptr(s string) *string { return &s }

func spec(t *testing.T, p domain.Provider) domain.Spec {
	t.Helper()
	s, ok := domain.SpecOf(p)
	if !ok {
		t.Fatalf("no spec for %s", p)
	}
	return s
}

func TestValidate(t *testing.T) {
	t.Parallel()
	token := ptr("EAAB1234567890abcdef")
	for _, tc := range []struct {
		name   string
		p      domain.Provider
		in     domain.PixelInput
		fields []string
	}{
		{"meta", domain.Meta, domain.PixelInput{PixelID: "123456789012345", TestCode: "TEST123", ServerToken: token}, nil},
		{"meta short id", domain.Meta, domain.PixelInput{PixelID: "12345"}, []string{"pixel_id"}},
		{"meta letters", domain.Meta, domain.PixelInput{PixelID: "12345678901234a"}, []string{"pixel_id"}},
		{"tiktok", domain.TikTok, domain.PixelInput{PixelID: "C4ABCDEF1234567890AB", ServerToken: token}, nil},
		{"tiktok lower case", domain.TikTok, domain.PixelInput{PixelID: "c4abcdef1234567890ab"}, []string{"pixel_id"}},
		{"snap", domain.Snap, domain.PixelInput{PixelID: "0f8fad5b-d9cb-469f-a165-70867728950e"}, nil},
		{"snap no test code", domain.Snap, domain.PixelInput{PixelID: "0f8fad5b-d9cb-469f-a165-70867728950e", TestCode: "T1"}, []string{"test_code"}},
		{"google ads", domain.GoogleAds, domain.PixelInput{PixelID: "AW-123456789", Label: "AbC-123_xyz"}, nil},
		{"google ads needs a label", domain.GoogleAds, domain.PixelInput{PixelID: "AW-123456789"}, []string{"label"}},
		{"google ads has no server API", domain.GoogleAds, domain.PixelInput{PixelID: "AW-123456789", Label: "AbC-123_xyz", ServerToken: token}, []string{"server_token"}},
		{"ga4", domain.GA4, domain.PixelInput{PixelID: "G-ABC123XYZ", ServerToken: ptr("secret-api-key-123")}, nil},
		{"ga4 takes no label", domain.GA4, domain.PixelInput{PixelID: "G-ABC123XYZ", Label: "AbC-123_xyz"}, []string{"label"}},
		{"removing a token", domain.Meta, domain.PixelInput{PixelID: "123456789012345", ServerToken: ptr("")}, nil},
		{"short token", domain.Meta, domain.PixelInput{PixelID: "123456789012345", ServerToken: ptr("abc")}, []string{"server_token"}},
		{"token with a space", domain.Meta, domain.PixelInput{PixelID: "123456789012345", ServerToken: ptr("abcd efgh ijkl")}, []string{"server_token"}},
		{"long token", domain.Meta, domain.PixelInput{PixelID: "123456789012345", ServerToken: ptr(strings.Repeat("a", domain.MaxTokenLen+1))}, []string{"server_token"}},
		{"bad test code", domain.Meta, domain.PixelInput{PixelID: "123456789012345", TestCode: "TEST 1"}, []string{"test_code"}},
		{"negative version", domain.Meta, domain.PixelInput{PixelID: "123456789012345", Version: -1}, []string{"version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.in.Validate(spec(t, tc.p))
			if tc.fields == nil {
				if err != nil {
					t.Fatalf("valid input refused: %v", err)
				}
				return
			}
			var e *errs.Error
			if !errs.Is(err, errs.KindValidation) || !asErr(err, &e) {
				t.Fatalf("want a validation error, got %v", err)
			}
			var got []string
			for _, f := range e.Fields {
				got = append(got, f.Field)
			}
			if strings.Join(got, ",") != strings.Join(tc.fields, ",") {
				t.Fatalf("fields %v, want %v", got, tc.fields)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	in := domain.PixelInput{PixelID: " 123456789012345\n", Label: " x ", TestCode: "\tT1 ", ServerToken: ptr("  tok  ")}.Normalize()
	if in.PixelID != "123456789012345" || in.Label != "x" || in.TestCode != "T1" || *in.ServerToken != "tok" {
		t.Fatalf("normalized %+v", in)
	}
	if (domain.PixelInput{}).Normalize().ServerToken != nil {
		t.Fatal("no token must stay no token")
	}
}

func TestSpecs(t *testing.T) {
	t.Parallel()
	var names []string
	for _, s := range domain.Specs() {
		names = append(names, string(s.Provider))
		for _, o := range append(append(append([]string{}, s.Script...), s.Connect...), s.Frame...) {
			if !strings.HasPrefix(o, "https://") || strings.ContainsAny(o, " ;'") {
				t.Errorf("%s: origin %q is not a plain https origin", s.Provider, o)
			}
		}
	}
	if strings.Join(names, ",") != "meta,tiktok,snap,google_ads,ga4" {
		t.Fatalf("providers %v", names)
	}
	if _, ok := domain.SpecOf("gtm"); ok {
		t.Fatal("an unknown provider has a spec")
	}
}

func TestBrowserOf(t *testing.T) {
	t.Parallel()
	pixels := []domain.Pixel{
		{Provider: domain.GA4, PixelID: "G-ABC123XYZ", Enabled: true, HasServerToken: true},
		{Provider: domain.Meta, PixelID: "123456789012345", Enabled: true},
		{Provider: domain.TikTok, PixelID: "C4ABCDEF1234567890AB", Enabled: false},
		{Provider: domain.GoogleAds, PixelID: "AW-123456789", Label: "AbC-123_xyz", Enabled: true},
	}
	b := domain.BrowserOf(pixels, domain.Settings{Consent: domain.ConsentRequired})
	var got []string
	for _, p := range b.Pixels {
		got = append(got, string(p.Provider)+"="+p.PixelID+"/"+p.Label)
	}
	// the registry's order; the disabled pixel is left out
	if strings.Join(got, " ") != "meta=123456789012345/ google_ads=AW-123456789/AbC-123_xyz ga4=G-ABC123XYZ/" {
		t.Fatalf("pixels %v", got)
	}
	if b.Consent != domain.ConsentRequired {
		t.Fatalf("consent %q", b.Consent)
	}
	// gtag's origins once, though two providers use it; TikTok's none
	if n := strings.Count(strings.Join(b.Script, " "), "googletagmanager"); n != 1 {
		t.Fatalf("script origins %v", b.Script)
	}
	if strings.Contains(strings.Join(b.Script, " "), "tiktok") || len(b.Frame) != 2 {
		t.Fatalf("origins script %v frame %v", b.Script, b.Frame)
	}
	if e := domain.BrowserOf(nil, domain.DefaultSettings()); len(e.Pixels)+len(e.Script)+len(e.Connect)+len(e.Frame) != 0 || e.Consent != domain.ConsentAuto {
		t.Fatalf("no pixels: %+v", e)
	}
}

func TestConsentValid(t *testing.T) {
	t.Parallel()
	for c, want := range map[domain.Consent]bool{"auto": true, "required": true, "off": true, "": false, "always": false} {
		if c.Valid() != want {
			t.Errorf("%q valid = %v", c, !want)
		}
	}
}

func asErr(err error, target **errs.Error) bool { return errors.As(err, target) }
