package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

func TestCleanName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"  أحمد   محمد \n", "أحمد محمد", true},
		{"Sara\tAli", "Sara Ali", true},
		{"\u200bمنى\ufeff", "منى", true},
		{"", "", true},
		{strings.Repeat("ا", domain.MaxNameLen), strings.Repeat("ا", domain.MaxNameLen), true},
		{strings.Repeat("ا", domain.MaxNameLen+1), "", false},
		{"bad \xff utf8", "", false},
	}
	for _, tt := range tests {
		got, err := domain.CleanName(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("CleanName(%q) = %q, %v", tt.in, got, err)
		}
		if err != nil && !errs.Is(err, errs.KindValidation) {
			t.Errorf("CleanName(%q) error kind: %v", tt.in, err)
		}
	}
	if _, err := domain.CleanReason(strings.Repeat("x", domain.MaxReasonLen+1)); !errs.Is(err, errs.KindValidation) {
		t.Errorf("long reason: %v", err)
	}
}

func TestBlocked(t *testing.T) {
	t.Parallel()
	var c domain.Customer
	if c.Blocked() {
		t.Fatal("zero customer is blocked")
	}
}

func TestResolveQuery(t *testing.T) {
	t.Parallel()
	egMobile, err := phone.Parse("01012345678", phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		q      domain.Query
		phone  *phone.E164
		prefix string
		limit  int
		kind   errs.Kind // zero: no error
	}{
		{name: "empty lists all", q: domain.Query{}, limit: domain.DefaultLimit},
		{name: "national phone", q: domain.Query{Text: " 010 1234 5678 "}, phone: &egMobile, limit: domain.DefaultLimit},
		{name: "international phone", q: domain.Query{Text: "+20 10 1234 5678", Limit: 5}, phone: &egMobile, limit: 5},
		{name: "arabic digits", q: domain.Query{Text: "٠١٠١٢٣٤٥٦٧٨"}, phone: &egMobile, limit: domain.DefaultLimit},
		{name: "name folds", q: domain.Query{Text: "أحمد"}, prefix: "احمد", limit: domain.DefaultLimit},
		{name: "like wildcards are literal", q: domain.Query{Text: "a%_\\b"}, prefix: `a\%\_\\b`, limit: domain.DefaultLimit},
		{name: "one letter", q: domain.Query{Text: "أ"}, kind: errs.KindValidation},
		{name: "digits that are no phone", q: domain.Query{Text: "123"}, prefix: "123", limit: domain.DefaultLimit},
		{name: "too long", q: domain.Query{Text: strings.Repeat("ب", domain.MaxQueryLen+1)}, kind: errs.KindValidation},
		{name: "limit too big", q: domain.Query{Limit: domain.MaxListLimit + 1}, kind: errs.KindValidation},
		{name: "negative limit", q: domain.Query{Limit: -1}, kind: errs.KindValidation},
		{name: "blocked only", q: domain.Query{Blocked: true}, limit: domain.DefaultLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, err := domain.ResolveQuery(tt.q, phone.EG)
			if tt.kind != 0 {
				if !errs.Is(err, tt.kind) {
					t.Fatalf("err = %v, want kind %v", err, tt.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (l.Phone == nil) != (tt.phone == nil) || (l.Phone != nil && *l.Phone != *tt.phone) ||
				l.NamePrefix != tt.prefix || l.Limit != tt.limit || l.Blocked != tt.q.Blocked {
				t.Fatalf("lookup = %+v", l)
			}
		})
	}
}

// FuzzResolveQuery: any text is accepted as a phone, a LIKE-safe name
// prefix, or a validation error; it never panics, and a name prefix has
// no unescaped wildcard.
func FuzzResolveQuery(f *testing.F) {
	for _, s := range []string{"", "01012345678", "أحمد", "%", "_x", `\`, "+966501234567", "\xff", "ـــ"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l, err := domain.ResolveQuery(domain.Query{Text: s}, phone.EG)
		if err != nil {
			if !errs.Is(err, errs.KindValidation) {
				t.Fatalf("ResolveQuery(%q): %v", s, err)
			}
			return
		}
		if l.Phone != nil && l.NamePrefix != "" {
			t.Fatalf("both phone and name for %q", s)
		}
		p := l.NamePrefix
		for i := 0; i < len(p); i++ {
			switch p[i] {
			case '\\':
				i++ // escaped character
				if i == len(p) {
					t.Fatalf("dangling escape in %q", p)
				}
			case '%', '_':
				t.Fatalf("unescaped wildcard in %q", p)
			}
		}
		if !utf8.ValidString(p) {
			t.Fatalf("invalid prefix %q", p)
		}
	})
}
