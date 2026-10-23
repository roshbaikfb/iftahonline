package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"time"
	"unicode"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Connection statuses.
const (
	ConnectionActive       = "active"
	ConnectionDisconnected = "disconnected"
)

// MaxAPIKeyLen bounds a carrier API key as pasted by a merchant.
const MaxAPIKeyLen = 512

// Connection is a store's account at a carrier. Its credentials are
// never part of it: they stay sealed until a job opens them.
type Connection struct {
	ID             ulid.ID
	Carrier        string
	Status         string
	CreatedAt      time.Time
	DisconnectedAt *time.Time
}

// CleanAPIKey trims a pasted key and checks it is one printable token.
func CleanAPIKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	bad := k == "" || len(k) > MaxAPIKeyLen || strings.IndexFunc(k, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsSpace(r) }) >= 0
	if bad {
		return "", errs.Validation(errs.FieldError{Field: "api_key", Code: "invalid", Message: "paste the API key from your carrier account"})
	}
	return k, nil
}

// SecretHash is what is kept of a webhook secret.
func SecretHash(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// SecretMatches compares a presented secret with a kept hash in constant
// time.
func SecretMatches(presented string, hash []byte) bool {
	return presented != "" && subtle.ConstantTimeCompare(SecretHash(presented), hash) == 1
}
