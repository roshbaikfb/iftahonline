package clientinfo_test

import (
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
)

func TestIPPrefix(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"203.0.113.77":         "203.0.113.0/24",
		"::ffff:203.0.113.77":  "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1::/48",
	}
	for in, want := range tests {
		if got := clientinfo.IPPrefix(netip.MustParseAddr(in)); got != want {
			t.Fatalf("IPPrefix(%s) = %q, want %q", in, got, want)
		}
	}
	if clientinfo.IPPrefix(netip.Addr{}) != "" {
		t.Fatal("invalid address")
	}
}

func TestTruncateUserAgent(t *testing.T) {
	t.Parallel()
	if got := clientinfo.TruncateUserAgent("Mozilla/5.0"); got != "Mozilla/5.0" {
		t.Fatal(got)
	}
	long := strings.Repeat("م", 150) // 300 bytes
	got := clientinfo.TruncateUserAgent(long)
	if len(got) > 200 || !utf8.ValidString(got) || len(got) < 198 {
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}
