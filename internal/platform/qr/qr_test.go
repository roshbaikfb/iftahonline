package qr_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/qr"
)

func TestSVG(t *testing.T) {
	t.Parallel()
	svg, err := qr.SVG("otpauth://totp/iftahonline:mona%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=iftahonline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `) || !strings.HasSuffix(svg, `"/></svg>`) {
		t.Fatalf("svg = %.80q…", svg)
	}
	// Only drawing: no text, scripts or links can come through.
	for _, bad := range []string{"<script", "<text", "href", "mona"} {
		if strings.Contains(svg, bad) {
			t.Fatalf("svg contains %q", bad)
		}
	}
	if !strings.Contains(svg, "h1v1h-1z") {
		t.Fatal("no dark modules")
	}
}

func TestSVGTooLong(t *testing.T) {
	t.Parallel()
	if _, err := qr.SVG(strings.Repeat("x", 5000)); err == nil {
		t.Fatal("a text beyond a QR code's capacity was drawn")
	}
}
