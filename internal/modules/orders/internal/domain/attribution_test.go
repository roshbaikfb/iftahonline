package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
)

func TestAttributionClean(t *testing.T) {
	t.Parallel()
	got := domain.Attribution{Source: " facebook ", Medium: "paid\nsocial", Campaign: strings.Repeat("ع", 150), Content: "\u200bad-1", Term: "\xff"}.Clean()
	if got.Source != "facebook" || got.Medium != "paid social" || utf8.RuneCountInString(got.Campaign) != domain.MaxUTMLen || got.Content != "ad-1" || got.Term != "" {
		t.Fatalf("cleaned %+v", got)
	}
	if !(domain.Attribution{}).Clean().IsZero() || got.IsZero() {
		t.Fatal("IsZero")
	}
}

func TestSignalsClean(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   domain.Signals
		want domain.Signals
	}{
		{"kept", domain.Signals{FBP: "fb.1.1730000000000.123", FBC: "fb.1.1730000000000.IwAR0abc", TTP: "abc_DEF-1", TTCLID: "E.C.P.x", ScCID: "a1b2", GCLID: "Cj0KCQ",
			IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (Linux; Android 14)"},
			domain.Signals{FBP: "fb.1.1730000000000.123", FBC: "fb.1.1730000000000.IwAR0abc", TTP: "abc_DEF-1", TTCLID: "E.C.P.x", ScCID: "a1b2", GCLID: "Cj0KCQ",
				IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (Linux; Android 14)"}},
		{"IPv4 in IPv6", domain.Signals{IP: "::ffff:203.0.113.7"}, domain.Signals{IP: "203.0.113.7"}},
		{"IPv6", domain.Signals{IP: " 2001:db8::1 "}, domain.Signals{IP: "2001:db8::1"}},
		{"IPv6 zone dropped", domain.Signals{IP: "fe80::1%eth0"}, domain.Signals{IP: "fe80::1"}},
		{"not an IP", domain.Signals{IP: "localhost"}, domain.Signals{}},
		{"ID with a space", domain.Signals{FBP: "fb 1"}, domain.Signals{}},
		{"ID not ASCII", domain.Signals{GCLID: "عربي"}, domain.Signals{}},
		{"ID too long", domain.Signals{TTCLID: strings.Repeat("a", domain.MaxSignalLen+1)}, domain.Signals{}},
		{"user agent cut", domain.Signals{UserAgent: strings.Repeat("u", 600)}, domain.Signals{UserAgent: strings.Repeat("u", domain.MaxUserAgentLen)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.Clean(); got != tc.want {
				t.Fatalf("cleaned %+v, want %+v", got, tc.want)
			}
		})
	}
}

// FuzzSignalsClean: whatever a browser sends, what is kept is bounded,
// printable where it must be, and cleaning is stable.
func FuzzSignalsClean(f *testing.F) {
	f.Add("fb.1.2.3", "203.0.113.7", "Mozilla/5.0", "facebook")
	f.Add("\x00\xff", "::ffff:1.2.3.4", "\n\t", "\u200b")
	f.Fuzz(func(t *testing.T, id, ip, ua, utm string) {
		s := domain.Signals{FBP: id, FBC: id, TTP: id, TTCLID: id, ScCID: id, GCLID: id, IP: ip, UserAgent: ua}.Clean()
		a := domain.Attribution{Source: utm, Medium: utm, Campaign: utm, Content: utm, Term: utm}.Clean()
		if len(s.FBP) > domain.MaxSignalLen || utf8.RuneCountInString(s.UserAgent) > domain.MaxUserAgentLen || utf8.RuneCountInString(a.Source) > domain.MaxUTMLen {
			t.Fatalf("too long: %+v %+v", s, a)
		}
		if strings.ContainsAny(s.FBP+s.IP, " \n\t\x00") || strings.ContainsAny(a.Source+s.UserAgent, "\n\t\x00") {
			t.Fatalf("not one clean line: %+v %+v", s, a)
		}
		if s.Clean() != s || a.Clean() != a {
			t.Fatalf("not stable: %+v %+v", s, a)
		}
	})
}
