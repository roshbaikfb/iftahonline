package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestNormalizeHost(t *testing.T) {
	t.Parallel()
	long63 := strings.Repeat("a", 63)
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"shop.iftahonline.com", "shop.iftahonline.com", true},
		{"Shop.IftahOnline.COM", "shop.iftahonline.com", true},
		{"shop.iftahonline.com:443", "shop.iftahonline.com", true},
		{"shop.iftahonline.com.", "shop.iftahonline.com", true},
		{"shop.iftahonline.com.:8080", "shop.iftahonline.com", true},
		{"متجر.مصر", "xn--pgbep1f.xn--wgbh1c", true},
		{"BÜCHER.example", "xn--bcher-kva.example", true},
		{"xn--bcher-kva.example", "xn--bcher-kva.example", true},
		{"a-b.c1", "a-b.c1", true},
		{long63 + ".com", long63 + ".com", true},
		{"localhost", "localhost", true},

		{"", "", false},
		{".", "", false},
		{"shop..com", "", false},
		{".shop.com", "", false},
		{"-shop.com", "", false},
		{"shop-.com", "", false},
		{"sh_op.com", "", false},
		{"shop.com:", "", false},
		{"shop.com:abc", "", false},
		{"shop.com:123456", "", false},
		{"127.0.0.1", "", false},
		{"127.0.0.1:80", "", false},
		{"[::1]", "", false},
		{"[::1]:443", "", false},
		{"::1", "", false},
		{"1.2.3", "", false},
		{"shop.123", "", false},
		{strings.Repeat("a", 64) + ".com", "", false},
		{strings.Repeat("abcdefghi.", 26) + "com", "", false}, // 263 bytes
		{"shop com", "", false},
		{"shop.com/path", "", false},
		{"user@shop.com", "", false},
		{"\x00.com", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NormalizeHost(tt.in)
			if tt.ok {
				if err != nil || got != tt.want {
					t.Fatalf("NormalizeHost(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
				}
				return
			}
			if !errors.Is(err, domain.ErrInvalidHost) {
				t.Fatalf("NormalizeHost(%q) = %q, %v; want ErrInvalidHost", tt.in, got, err)
			}
		})
	}
}

func TestNormalizeHostDoesNotAllocateOnCommonPath(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := domain.NormalizeHost("my-shop.iftahonline.com:443"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

func FuzzNormalizeHost(f *testing.F) {
	for _, s := range []string{"shop.iftahonline.com", "A.B:1", "متجر.مصر", "[::1]", "1.2.3.4", "xn--", "a..b", "-a.b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		h, err := domain.NormalizeHost(raw)
		if err != nil {
			return
		}
		// Output is canonical: normalizing again is a no-op.
		again, err := domain.NormalizeHost(h)
		if err != nil || again != h {
			t.Fatalf("not idempotent: %q -> %q -> %q, %v", raw, h, again, err)
		}
		if len(h) > 253 || strings.ContainsAny(h, ":[]/@ ") || strings.ToLower(h) != h {
			t.Fatalf("bad output %q from %q", h, raw)
		}
		for i := 0; i < len(h); i++ {
			if h[i] >= 0x80 {
				t.Fatalf("non-ASCII output %q", h)
			}
		}
	})
}

func fieldCodes(err error) map[string]string {
	e, ok := errors.AsType[*errs.Error](err)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, f := range e.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func TestValidateNewStore(t *testing.T) {
	t.Parallel()
	valid := domain.NewStore{Slug: " My-Shop ", Name: "  متجر أحمد ", Currency: "egp", Locale: "ar", Timezone: "Africa/Cairo"}
	got, err := domain.ValidateNewStore(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "my-shop" || got.Name != "متجر أحمد" || got.Currency != "EGP" {
		t.Fatalf("normalized = %+v", got)
	}

	tests := []struct {
		name   string
		mutate func(*domain.NewStore)
		want   map[string]string
	}{
		{"short slug", func(s *domain.NewStore) { s.Slug = "ab" }, map[string]string{"slug": "invalid"}},
		{"slug with dot", func(s *domain.NewStore) { s.Slug = "my.shop" }, map[string]string{"slug": "invalid"}},
		{"slug double hyphen", func(s *domain.NewStore) { s.Slug = "my--shop" }, map[string]string{"slug": "invalid"}},
		{"slug punycode", func(s *domain.NewStore) { s.Slug = "xn--abc" }, map[string]string{"slug": "invalid"}},
		{"slug leading hyphen", func(s *domain.NewStore) { s.Slug = "-shop" }, map[string]string{"slug": "invalid"}},
		{"slug unicode", func(s *domain.NewStore) { s.Slug = "متجر" }, map[string]string{"slug": "invalid"}},
		{"slug all digits", func(s *domain.NewStore) { s.Slug = "123" }, nil},
		{"slug reserved", func(s *domain.NewStore) { s.Slug = "admin" }, map[string]string{"slug": "reserved"}},
		{"empty name", func(s *domain.NewStore) { s.Name = "   " }, map[string]string{"name": "length"}},
		{"long name", func(s *domain.NewStore) { s.Name = strings.Repeat("م", 121) }, map[string]string{"name": "length"}},
		{"control char", func(s *domain.NewStore) { s.Name = "a\u0007b" }, map[string]string{"name": "invalid"}},
		{"bad currency", func(s *domain.NewStore) { s.Currency = "USD" }, map[string]string{"currency": "unsupported"}},
		{"bad locale", func(s *domain.NewStore) { s.Locale = "fr" }, map[string]string{"locale": "unsupported"}},
		{"bad timezone", func(s *domain.NewStore) { s.Timezone = "Europe/Paris" }, map[string]string{"timezone": "unsupported"}},
		{"everything wrong", func(s *domain.NewStore) { *s = domain.NewStore{} },
			map[string]string{"slug": "invalid", "name": "length", "currency": "unsupported", "locale": "unsupported", "timezone": "unsupported"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := valid
			tt.mutate(&in)
			_, err := domain.ValidateNewStore(in)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errs.Is(err, errs.KindValidation) {
				t.Fatalf("err = %v, want validation", err)
			}
			got := fieldCodes(err)
			if len(got) != len(tt.want) {
				t.Fatalf("fields = %v, want %v", got, tt.want)
			}
			for f, c := range tt.want {
				if got[f] != c {
					t.Fatalf("fields = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestValidateSettings(t *testing.T) {
	t.Parallel()
	got, err := domain.ValidateSettings(domain.StoreSettings{Name: " New ", Locale: "en", Timezone: "Asia/Dubai"})
	if err != nil || got.Name != "New" {
		t.Fatalf("got %+v, %v", got, err)
	}
	_, err = domain.ValidateSettings(domain.StoreSettings{Name: "", Locale: "xx", Timezone: "Mars/Base"})
	if f := fieldCodes(err); len(f) != 3 {
		t.Fatalf("fields = %v", f)
	}
}

func TestParseStoreStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"active", "suspended", "closed"} {
		if _, ok := domain.ParseStoreStatus(s); !ok {
			t.Fatalf("%q must parse", s)
		}
	}
	if _, ok := domain.ParseStoreStatus("deleted"); ok {
		t.Fatal("unknown status must not parse")
	}
}

func TestValidateCustomDomain(t *testing.T) {
	t.Parallel()
	const base = "iftahonline.com"
	tests := []struct {
		in, want, code string
	}{
		{"Shop.Example.com", "shop.example.com", ""},
		{" shop.example.com. ", "shop.example.com", ""},
		{"متجر.مصر", "xn--pgbep1f.xn--wgbh1c", ""},
		{"example", "", "invalid"},
		{"shop.example.com:443", "", "invalid"},
		{"https://shop.example.com", "", "invalid"},
		{"shop.example.com/x", "", "invalid"},
		{"1.2.3.4", "", "invalid"},
		{"iftahonline.com", "", "reserved"},
		{"evil.iftahonline.com", "", "reserved"},
		{"IFTAHONLINE.COM", "", "reserved"},
		{"notiftahonline.com", "notiftahonline.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ValidateCustomDomain(tt.in, base)
			if tt.code == "" {
				if err != nil || got != tt.want {
					t.Fatalf("got %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if fieldCodes(err)["host"] != tt.code {
				t.Fatalf("got %q, %v; want code %q", got, err, tt.code)
			}
		})
	}
}

func TestVerification(t *testing.T) {
	t.Parallel()
	tok, err := domain.NewVerificationToken()
	if err != nil {
		t.Fatal(err)
	}
	tok2, _ := domain.NewVerificationToken()
	if len(tok) != 32 || tok == tok2 || strings.ToLower(tok) != tok {
		t.Fatalf("tokens %q %q", tok, tok2)
	}
	d := domain.Domain{Host: "shop.example.com", VerificationToken: tok}
	name, value := d.VerificationRecord()
	if name != "_iftah-verify.shop.example.com" || value != "iftah-verify="+tok {
		t.Fatalf("record = %q %q", name, value)
	}
	if !d.TXTProvesOwnership([]string{"v=spf1 -all", " iftah-verify=" + tok + " "}) {
		t.Fatal("matching TXT must prove ownership")
	}
	if d.TXTProvesOwnership([]string{"iftah-verify=" + tok2, "iftah-verify=", tok}) {
		t.Fatal("other values must not prove ownership")
	}
	if domain.SubdomainHost("shop", "iftahonline.com") != "shop.iftahonline.com" {
		t.Fatal("subdomain host")
	}
}
