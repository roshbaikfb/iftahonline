package totp_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
)

// TestHOTPVectors uses RFC 6238 Appendix B (SHA-1, seed "1234567890…"):
// the 6-digit code is the last 6 digits of the published 8-digit value.
func TestHOTPVectors(t *testing.T) {
	t.Parallel()
	secret := []byte("12345678901234567890")
	vectors := map[int64]string{
		59:          "94287082",
		1111111109:  "07081804",
		1111111111:  "14050471",
		1234567890:  "89005924",
		2000000000:  "69279037",
		20000000000: "65353130",
	}
	for unix, want8 := range vectors {
		step := totp.Step(time.Unix(unix, 0))
		if got := totp.HOTP(secret, step, 8); got != want8 {
			t.Errorf("t=%d: 8 digits = %s, want %s", unix, got, want8)
		}
		if got := totp.HOTP(secret, step, 6); got != want8[2:] {
			t.Errorf("t=%d: 6 digits = %s, want %s", unix, got, want8[2:])
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	t.Parallel()
	secret, err := totp.NewSecret()
	if err != nil || len(secret) != 20 {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_015, 0)
	step := totp.Step(now)
	code := totp.HOTP(secret, step, 6)

	if got, ok := totp.Verify(secret, code, now, 0); !ok || got != step {
		t.Fatalf("current = %d %v", got, ok)
	}
	if _, ok := totp.Verify(secret, " "+code+" ", now, 0); !ok {
		t.Fatal("surrounding spaces must be accepted")
	}
	// Replay of the same step is refused.
	if _, ok := totp.Verify(secret, code, now, step); ok {
		t.Fatal("replay accepted")
	}
	// One step of drift either way is accepted, two are not.
	if _, ok := totp.Verify(secret, totp.HOTP(secret, step-1, 6), now, 0); !ok {
		t.Fatal("previous step refused")
	}
	if _, ok := totp.Verify(secret, totp.HOTP(secret, step+1, 6), now, 0); !ok {
		t.Fatal("next step refused")
	}
	if _, ok := totp.Verify(secret, totp.HOTP(secret, step-2, 6), now, 0); ok {
		t.Fatal("two steps old accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := totp.Verify(secret, bad, now, 0); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
	other, _ := totp.NewSecret()
	if _, ok := totp.Verify(other, code, now, 0); ok && totp.HOTP(other, step, 6) != code {
		t.Fatal("another secret's code accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	t.Parallel()
	u, err := url.Parse(totp.URI([]byte("12345678901234567890"), "iftahonline", "ahmed@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/iftahonline:ahmed@example.com" ||
		q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "iftahonline" ||
		q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Fatalf("uri = %s", u)
	}
}

func FuzzVerifyTOTP(f *testing.F) {
	secret := []byte("12345678901234567890")
	f.Add("287082", int64(59))
	f.Add("", int64(0))
	f.Fuzz(func(t *testing.T, code string, unix int64) {
		if unix < 0 || unix > 1<<40 {
			return
		}
		now := time.Unix(unix, 0)
		step, ok := totp.Verify(secret, code, now, 0)
		if ok && (step < totp.Step(now)-1 || step > totp.Step(now)+1 || totp.HOTP(secret, step, 6) != strings.TrimSpace(code)) {
			t.Fatalf("accepted %q at %d with step %d", code, unix, step)
		}
	})
}
