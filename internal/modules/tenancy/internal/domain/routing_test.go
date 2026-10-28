package domain_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
)

func TestIsApex(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"shop.example.com", false},
		{"example.com.eg", true},
		{"shop.example.com.eg", false},
		{"example.co.uk", true},
		{"متجر.مصر", true}, // not normalized: still judged by its suffix
		{"xn--mgbh0fb.xn--wgbh1c", true},
		{"a.b.example.sa", false},
		{"com", false},       // a public suffix is no one's apex
		{"com.eg", false},    // nor is a two-label suffix
		{"localhost", false}, // a single label
		{"", false},
	}
	for _, c := range cases {
		if got := domain.IsApex(c.host); got != c.want {
			t.Errorf("IsApex(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestEdgeRoutingFor(t *testing.T) {
	edge := domain.Edge{
		Target: "connect.iftahonline.com",
		IPv4:   []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")},
		IPv6:   []netip.Addr{netip.MustParseAddr("2001:db8::10")},
	}
	type rec = domain.DNSRecord
	cases := []struct {
		name string
		edge domain.Edge
		host string
		apex bool
		want []rec
	}{
		{"subdomain: one CNAME", edge, "shop.example.com", false, []rec{{Type: "CNAME", Name: "shop.example.com", Value: "connect.iftahonline.com"}}},
		{"apex: ALIAS, then A and AAAA", edge, "example.com", true, []rec{
			{Type: "ALIAS", Name: "example.com", Value: "connect.iftahonline.com"},
			{Type: "A", Name: "example.com", Value: "203.0.113.10"},
			{Type: "A", Name: "example.com", Value: "203.0.113.11"},
			{Type: "AAAA", Name: "example.com", Value: "2001:db8::10"},
		}},
		{"apex without addresses: ALIAS only", domain.Edge{Target: "connect.iftahonline.com"}, "example.com.eg", true, []rec{
			{Type: "ALIAS", Name: "example.com.eg", Value: "connect.iftahonline.com"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.edge.RoutingFor(c.host)
			if got.Apex != c.apex || !slices.Equal(got.Records, c.want) {
				t.Errorf("RoutingFor(%q) = %+v, want apex=%v %+v", c.host, got, c.apex, c.want)
			}
		})
	}
}

func FuzzIsApex(f *testing.F) {
	for _, s := range []string{"example.com", "shop.example.com.eg", "com", "", ".", "a..b", "xn--", "..com"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, host string) {
		if domain.IsApex(host) {
			// An apex has a parent that is a public suffix: dropping its
			// first label never leaves another apex.
			i := 0
			for i < len(host) && host[i] != '.' {
				i++
			}
			if i < len(host) && domain.IsApex(host[i+1:]) {
				t.Errorf("IsApex(%q) and IsApex(%q)", host, host[i+1:])
			}
		}
	})
}
