package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"math/big"
	"strings"
	"time"
)

func b32() *base32.Encoding { return base32.StdEncoding.WithPadding(base32.NoPadding) }

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
