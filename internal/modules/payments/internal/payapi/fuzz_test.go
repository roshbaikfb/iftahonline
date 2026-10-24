package payapi_test

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/payapi"
)

// FuzzCheckReturn: the return URL comes from whoever calls the route.
// What is accepted is an http(s) URL with a host and no credentials,
// within the size bound, and what the shopper is sent to is that URL
// (its host the one checked), never another.
func FuzzCheckReturn(f *testing.F) {
	for _, s := range []string{
		"https://souq.iftahonline.com/thank-you/01J9ZQ3W5Y8V4K2N6M7P0R1S2T", "http://localhost:18081/thank-you/x", "javascript:alert(1)",
		"//evil.example/x", "https://user:pw@souq.test/", "https://souq.test\\@evil.example/", "https://[::1]:8443/x", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, back string) {
		u, ok := payapi.CheckReturn(back)
		if !ok {
			return
		}
		if len(back) > payapi.MaxReturnURL || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			t.Fatalf("accepted %q: %+v", back, u)
		}
		// The redirect goes to u.String(): it must point at the host checked.
		again, err := url.Parse(u.String())
		if err != nil || again.Host != u.Host || again.User != nil {
			t.Fatalf("%q: checked host %q, redirect to %q", back, u.Host, u.String())
		}
		// The way back carries it base64url-encoded, whole.
		enc := base64.RawURLEncoding.EncodeToString([]byte(u.String()))
		if dec, decErr := base64.RawURLEncoding.DecodeString(enc); decErr != nil || string(dec) != u.String() || strings.ContainsAny(enc, "/?#") {
			t.Fatalf("%q does not survive the return path", back)
		}
	})
}
