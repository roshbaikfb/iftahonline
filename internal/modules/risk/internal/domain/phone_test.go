package domain_test

import (
	"bytes"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

func TestKeyer(t *testing.T) {
	t.Parallel()
	if _, err := domain.NewKeyer(make([]byte, 31)); err == nil {
		t.Fatal("a short secret must fail")
	}
	secret := bytes.Repeat([]byte("k"), 32)
	k, err := domain.NewKeyer(secret)
	if err != nil {
		t.Fatal(err)
	}
	secret[0] = 'x' // the keyer keeps its own copy
	p := func(raw string) phone.E164 {
		t.Helper()
		e, perr := phone.Parse(raw, phone.Region("EG"))
		if perr != nil {
			t.Fatal(perr)
		}
		return e
	}
	a, b, c := k.Key(p("01012345678")), k.Key(p("+20 10 1234 5678")), k.Key(p("01012345679"))
	if len(a) != domain.PhoneKeyLen || !bytes.Equal(a, b) || bytes.Equal(a, c) {
		t.Fatalf("keys %x %x %x", a, b, c)
	}
	if bytes.Contains(a, []byte("1012345678")) {
		t.Fatal("the key holds the number")
	}
	other, err := domain.NewKeyer(bytes.Repeat([]byte("o"), 32))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(other.Key(p("01012345678")), a) {
		t.Fatal("another secret gives the same key")
	}
}
