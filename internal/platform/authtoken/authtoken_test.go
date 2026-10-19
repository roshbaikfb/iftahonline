package authtoken_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
)

func TestToken(t *testing.T) {
	t.Parallel()
	a, err := authtoken.New()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := authtoken.New()
	if a == b || len(a) != 43 {
		t.Fatalf("tokens %q %q", a, b)
	}
	if p, ok := authtoken.Parse(string(a)); !ok || p != a {
		t.Fatal("round trip")
	}
	for _, bad := range []string{"", "short", string(a) + "x", strings.Repeat("!", 43), strings.Repeat("A", 42) + "="} {
		if _, ok := authtoken.Parse(bad); ok {
			t.Fatalf("%q must not parse", bad)
		}
	}
	if len(a.Hash()) != 32 || string(a.Hash()) == string(b.Hash()) {
		t.Fatal("hash")
	}
	if a.String() == string(a) || strings.Contains(a.String(), string(a)) {
		t.Fatal("String must not reveal the token")
	}
}

func FuzzParseToken(f *testing.F) {
	tok, _ := authtoken.New()
	f.Add(string(tok))
	f.Add("")
	f.Add(strings.Repeat("A", 43))
	f.Fuzz(func(t *testing.T, s string) {
		if p, ok := authtoken.Parse(s); ok && (string(p) != s || len(p.Hash()) != 32) {
			t.Fatalf("bad parse of %q", s)
		}
	})
}
