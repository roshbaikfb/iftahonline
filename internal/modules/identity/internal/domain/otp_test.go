package domain_test

import (
	"crypto/hmac"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
)

func TestRecoveryCodes(t *testing.T) {
	t.Parallel()
	codes, err := domain.NewRecoveryCodes(domain.RecoveryCodeCount)
	if err != nil || len(codes) != 10 {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 19 || strings.Count(c, "-") != 3 || seen[c] || !domain.LooksLikeRecoveryCode(c) {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
	}
	c := codes[0]
	if !hmac.Equal(domain.HashRecoveryCode(c), domain.HashRecoveryCode(" "+strings.ToUpper(strings.ReplaceAll(c, "-", ""))+" ")) {
		t.Fatal("normalization")
	}
	if hmac.Equal(domain.HashRecoveryCode(codes[0]), domain.HashRecoveryCode(codes[1])) {
		t.Fatal("distinct codes collide")
	}
	if domain.LooksLikeRecoveryCode("123456") {
		t.Fatal("a TOTP code is not a recovery code")
	}
}

func TestOTP(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 200 {
		c, err := domain.NewOTP()
		if err != nil || len(c) != 6 || strings.Trim(c, "0123456789") != "" {
			t.Fatalf("otp %q %v", c, err)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes in 200", len(seen))
	}
	pep := []byte("pepper-pepper-pepper-pepper-pepp")
	a := domain.OTPMAC(pep, "ch1", "login", "123456")
	if !hmac.Equal(a, domain.OTPMAC(pep, "ch1", "login", " 123456 ")) {
		t.Fatal("trim")
	}
	for name, b := range map[string][]byte{
		"other challenge": domain.OTPMAC(pep, "ch2", "login", "123456"),
		"other purpose":   domain.OTPMAC(pep, "ch1", "verify_phone", "123456"),
		"other pepper":    domain.OTPMAC([]byte("x"), "ch1", "login", "123456"),
		"other code":      domain.OTPMAC(pep, "ch1", "login", "123457"),
	} {
		if hmac.Equal(a, b) {
			t.Fatalf("%s: MAC must differ", name)
		}
	}
}
