// Package authtoken makes opaque session tokens (ADR-0005): 256 random
// bits shown to the client once, of which only the SHA-256 is stored.
// Merchant sessions (identity) and admin sessions use it.
package authtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Len is the length of a token's base64url (unpadded) form.
const Len = 43

// Token is a session secret.
type Token string

// New returns a fresh random token.
func New() (Token, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("authtoken: %w", err)
	}
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

// Parse accepts only well-formed tokens, so junk never reaches the
// database.
func Parse(s string) (Token, bool) {
	if len(s) != Len {
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
