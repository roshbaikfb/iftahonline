package web

import (
	"net/url"
	"testing"
)

// FuzzSourceOf: the campaign cookie is the shopper's to write; any value
// reads as a campaign or as none, never a panic, and a campaign app.js
// wrote reads back whole.
func FuzzSourceOf(f *testing.F) {
	for _, s := range []string{"", "utm_source%3Dfacebook%26fbclid%3Dabc%26ts%3D1761700000", "%zz", "a=%", "utm_source=x&utm_source=y", ";;;"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cookie string) {
		q := sourceOf(cookie)
		if q == nil {
			t.Fatal("nil values")
		}
		if back := sourceOf(url.QueryEscape(q.Encode())); back.Encode() != q.Encode() {
			t.Fatalf("round trip %q -> %q", q.Encode(), back.Encode())
		}
	})
}

// FuzzParseAdded: a pending add reads whole or not at all.
func FuzzParseAdded(f *testing.F) {
	for _, s := range []string{"01M5PAGE000000000000000001.01M5PAGE000000000000000002.3", "x", "a.b.c", "01M5PAGE000000000000000001.01M5PAGE000000000000000002.0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		id, _, qty, ok := parseAdded(value)
		if !ok {
			return
		}
		if len(id) != 26 || qty < 1 || qty > maxAddedQty {
			t.Fatalf("parseAdded(%q) = %q %d", value, id, qty)
		}
	})
}
