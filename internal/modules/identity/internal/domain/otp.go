package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1; authenticator apps expect it.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238 defaults every authenticator app supports).
const (
	TOTPPeriod     = 30 * time.Second
	TOTPDigits     = 6
	TOTPSecretSize = 20 // 160 bits, as RFC 4226 recommends
	// totpSkew accepts the previous and next step for clock drift.
	totpSkew = 1
)

func b32() *base32.Encoding { return base32.StdEncoding.WithPadding(base32.NoPadding) }

// NewTOTPSecret returns a random seed.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, TOTPSecretSize)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("identity: totp secret: %w", err)
	}
	return s, nil
}

// TOTPURI is the otpauth:// URI an authenticator app scans (as a QR code
// rendered by the dashboard).
func TOTPURI(secret []byte, issuer, account string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", b32().EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(int(TOTPPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPStep is the time step containing t.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod.Seconds()) }

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

// VerifyTOTP checks code against the steps around now. It returns the
// matching step, which must be stored and is refused next time
// (lastStep): a code can be used once, even within its 30 seconds.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	cur := TOTPStep(now)
	var matched int64
	ok := 0
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		// Check every candidate so timing does not depend on which matched.
		if subtle.ConstantTimeCompare([]byte(HOTP(secret, s, TOTPDigits)), []byte(code)) == 1 && s > lastStep {
			matched, ok = s, 1
		}
	}
	return matched, ok == 1
}

// RecoveryCodeCount is how many recovery codes enrolment issues.
const RecoveryCodeCount = 10

// NewRecoveryCodes returns n one-time codes like "k3j5-a9xq-2mzt-7c4v"
// (80 bits each).
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, n)
	for i := range out {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("identity: recovery code: %w", err)
		}
		s := strings.ToLower(b32().EncodeToString(b)) // 16 chars
		out[i] = s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
	}
	return out, nil
}

// HashRecoveryCode normalizes (case, spaces, dashes) and hashes a code.
func HashRecoveryCode(code string) []byte {
	n := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	h := sha256.Sum256([]byte(n))
	return h[:]
}

// LooksLikeRecoveryCode tells a recovery code from a TOTP code.
func LooksLikeRecoveryCode(code string) bool {
	return len(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code))) == 16
}

// OTP (SMS/WhatsApp) parameters (ADR-0005 §4).
const (
	OTPDigits      = 6
	OTPTTL         = 5 * time.Minute
	OTPMaxAttempts = 5
)

// NewOTP returns a uniformly random 6-digit code.
func NewOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("identity: otp: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// OTPMAC binds a code to its challenge and purpose under the server
// pepper; only this MAC is stored.
func OTPMAC(pepper []byte, challengeID, purpose, code string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(purpose + "\x00" + challengeID + "\x00" + strings.TrimSpace(code)))
	return m.Sum(nil)
}
