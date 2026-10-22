package csp_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
)

func TestStorefront(t *testing.T) {
	t.Parallel()
	base := csp.Storefront()
	for _, want := range []string{"script-src 'self';", "style-src 'self';", "object-src 'none'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(base, want) {
			t.Errorf("policy %q lacks %q", base, want)
		}
	}
	if strings.Contains(base, "unsafe-inline") || strings.Contains(base, "unsafe-eval") {
		t.Fatalf("unsafe source in %q", base)
	}
	with := csp.Storefront("'sha256-abc='", "'sha256-def='")
	if !strings.Contains(with, "style-src 'self' 'sha256-abc=' 'sha256-def=';") || !strings.Contains(with, "script-src 'self';") {
		t.Fatalf("hashes must extend style-src only: %q", with)
	}
	if !strings.Contains(csp.API, "default-src 'none'") {
		t.Fatalf("API policy %q", csp.API)
	}
}

// TestStorefrontChallenge: the challenge page alone may load Turnstile's
// script and frame; everything else stays as strict as Storefront.
func TestStorefrontChallenge(t *testing.T) {
	t.Parallel()
	got := csp.StorefrontChallenge("'sha256-abc'")
	for _, want := range []string{
		"script-src 'self' https://challenges.cloudflare.com",
		"frame-src https://challenges.cloudflare.com",
		"style-src 'self' 'sha256-abc'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"form-action 'self'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("policy lacks %q: %s", want, got)
		}
	}
	if strings.Contains(csp.Storefront("'sha256-abc'"), "cloudflare") {
		t.Fatal("the shared policy must not allow Cloudflare")
	}
}
