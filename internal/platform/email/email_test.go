package email_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/email"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"Ahmed@Example.COM", "ahmed@example.com", true},
		{"  a.b+tag@shop.example.eg ", "a.b+tag@shop.example.eg", true},
		{"", "", false},
		{"no-at.example.com", "", false},
		{"a@localhost", "", false},
		{"a@.example.com", "", false},
		{"a@example.com.", "", false},
		{"a@exa..mple.com", "", false},
		{"Ahmed <a@example.com>", "", false},
		{"a@[127.0.0.1]", "", false},
		{"a b@example.com", "", false},
		{"\"quoted\"@example.com", "", false},
		{strings.Repeat("a", 65) + "@example.com", "", false},
		{"a@" + strings.Repeat("b", 250) + ".com", "", false},
		{"a@example.com\n", "a@example.com", true},
		{"a@example.com\r\nBcc: x@y.com", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, ok := email.Normalize(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func FuzzNormalizeEmail(f *testing.F) {
	for _, s := range []string{"a@b.co", "A@B.CO", "x <a@b.co>", "a@b", "\"a\"@b.co", "a@b..co"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, ok := email.Normalize(raw)
		if !ok {
			return
		}
		again, ok2 := email.Normalize(got)
		if !ok2 || again != got {
			t.Fatalf("not idempotent: %q -> %q -> %q", raw, got, again)
		}
		if strings.ToLower(got) != got || strings.ContainsAny(got, " \r\n<>,;") || strings.Count(got, "@") != 1 || len(got) > 254 {
			t.Fatalf("bad output %q", got)
		}
	})
}
