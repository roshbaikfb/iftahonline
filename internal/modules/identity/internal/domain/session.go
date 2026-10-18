package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/netip"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Session lifetimes (ADR-0005).
const (
	SessionIdleTimeout = 7 * 24 * time.Hour  // no activity for this long ends the session
	SessionMaxLifetime = 30 * 24 * time.Hour // absolute cap, whatever the activity
	// SessionTouchEvery bounds last_seen_at writes to one per interval.
	SessionTouchEvery = 5 * time.Minute
)

// tokenBytes of randomness: 256 bits.
const tokenBytes = 32

// tokenLen is the length of the base64url (unpadded) form.
const tokenLen = 43

// Token is a session secret. It is shown to the client once and never
// stored: the database keeps only its SHA-256.
type Token string

// NewToken returns a fresh random token.
func NewToken() (Token, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("identity: token: %w", err)
	}
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

// ParseToken accepts only well-formed tokens, so junk never reaches the
// database.
func ParseToken(s string) (Token, bool) {
	if len(s) != tokenLen {
		return "", false
	}
	if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
		return "", false
	}
	return Token(s), true
}

// Hash is what the database stores and looks up.
func (t Token) Hash() []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

// String never reveals the token.
func (Token) String() string { return "[session token]" }

// Session is a signed-in device.
type Session struct {
	ID         ulid.ID
	UserID     ulid.ID
	UserAgent  string
	IPPrefix   string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// ExpiresAt is when a session created or touched at now ends: the idle
// timeout, capped by the absolute lifetime from creation.
func ExpiresAt(created, now time.Time) time.Time {
	idle := now.Add(SessionIdleTimeout)
	if limit := created.Add(SessionMaxLifetime); idle.After(limit) {
		return limit
	}
	return idle
}

// IPPrefix reduces an address to its /24 (IPv4) or /48 (IPv6) network:
// enough to recognise "same network" in a session list or audit entry,
// without storing a personal identifier.
func IPPrefix(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	a = a.Unmap()
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

// DeviceHash is what user_devices stores for a device identifier.
func DeviceHash(deviceID string) []byte {
	h := sha256.Sum256([]byte("device\x00" + deviceID))
	return h[:]
}

// NewDeviceID returns a random identifier for the device cookie.
func NewDeviceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("identity: device id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidDeviceID bounds client-supplied identifiers.
func ValidDeviceID(s string) bool {
	if len(s) < 16 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// TruncateUserAgent bounds what we keep of a User-Agent header.
func TruncateUserAgent(ua string) string {
	const maxLen = 200
	if len(ua) <= maxLen {
		return ua
	}
	// Cut on a rune boundary.
	for i := maxLen; i > 0; i-- {
		if ua[i]&0xC0 != 0x80 {
			return ua[:i]
		}
	}
	return ""
}
