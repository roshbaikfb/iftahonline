// Package totp implements RFC 6238 time-based one-time passwords (HMAC-
// SHA1, 6 digits, 30 s), the format every authenticator app supports.
// Merchants (identity) and platform admins both use it.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1; authenticator apps expect it.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Parameters (RFC 6238 defaults).
const (
	Period     = 30 * time.Second
	Digits     = 6
	SecretSize = 20 // 160 bits, as RFC 4226 recommends
	// skew accepts the previous and next step for clock drift.
	skew = 1
)

func b32() *base32.Encoding { return base32.StdEncoding.WithPadding(base32.NoPadding) }

// NewSecret returns a random seed.
func NewSecret() ([]byte, error) {
	s := make([]byte, SecretSize)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("totp: secret: %w", err)
	}
	return s, nil
}

// EncodeSecret is the base32 form users type into an app by hand.
func EncodeSecret(secret []byte) string { return b32().EncodeToString(secret) }

// URI is the otpauth:// URI an authenticator app scans as a QR code.
func URI(secret []byte, issuer, account string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(int(Period.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step is the time step containing t.
func Step(t time.Time) int64 { return t.Unix() / int64(Period.Seconds()) }

// HOTP computes the RFC 4226 code for counter.
func HOTP(secret []byte, counter int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // time steps are positive.
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// Verify checks code against the steps around now. It returns the
// matching step, which the caller must store and pass as lastStep next
// time: a code can be used once, even within its 30 seconds.
func Verify(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	var matched int64
	ok := 0
	for s := cur - skew; s <= cur+skew; s++ {
		// Check every candidate so timing does not depend on which matched.
		if subtle.ConstantTimeCompare([]byte(HOTP(secret, s, Digits)), []byte(code)) == 1 && s > lastStep {
			matched, ok = s, 1
		}
	}
	return matched, ok == 1
}
