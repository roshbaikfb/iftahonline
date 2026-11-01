package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// PhoneKeyLen is a phone key's length in bytes.
const PhoneKeyLen = sha256.Size

// Keyer turns phone numbers into the keys that stand for them across
// stores: HMAC-SHA256 of the E.164 number under RISK_PHONE_KEY. Without
// the key, a stored key cannot be turned back into a number by trying
// every number (ADR-0065).
type Keyer struct{ secret []byte }

// NewKeyer returns a Keyer; the secret must have at least 32 bytes.
func NewKeyer(secret []byte) (Keyer, error) {
	if len(secret) < 32 {
		return Keyer{}, errors.New("risk: the phone key needs at least 32 bytes")
	}
	return Keyer{secret: append([]byte(nil), secret...)}, nil
}

// Key returns p's key: the same in every store for the same number.
func (k Keyer) Key(p phone.E164) []byte {
	m := hmac.New(sha256.New, k.secret)
	m.Write([]byte(p.Unmasked()))
	return m.Sum(nil)
}
