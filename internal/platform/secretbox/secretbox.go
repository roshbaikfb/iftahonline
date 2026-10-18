// Package secretbox encrypts small secrets at rest (TOTP seeds, pending
// OTP codes, provider credentials) with AES-256-GCM under a versioned
// keyring, so keys can be rotated without re-encrypting everything at once.
//
// Sealed format: version (1 byte) || nonce (12 bytes) || ciphertext+tag.
//
// Every Seal/Open takes associated data (AAD) naming what the secret
// belongs to, e.g. "totp:<user id>". A ciphertext copied to another row
// then fails to open instead of silently working for the wrong user.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// KeySize is the required key length (AES-256).
const KeySize = 32

const nonceSize = 12

// ErrOpen means a sealed value is malformed, was sealed with an unknown
// key, or fails authentication (tampered, or wrong AAD).
var ErrOpen = errors.New("secretbox: cannot open")

// Keyring holds every key that may open data and the one used to seal.
type Keyring struct {
	current byte
	aeads   map[byte]cipher.AEAD
}

// ParseKeyring parses "v:base64key[,v:base64key...]" where v is 1..255.
// The first entry is the current (sealing) key; the others only open old
// data. Keys are standard or URL-safe base64 of exactly 32 random bytes.
func ParseKeyring(spec string) (*Keyring, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("secretbox: empty keyring")
	}
	k := &Keyring{aeads: map[byte]cipher.AEAD{}}
	for i, entry := range strings.Split(spec, ",") {
		vs, b64, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			return nil, fmt.Errorf("secretbox: entry %d: want version:key", i+1)
		}
		v, err := strconv.ParseUint(vs, 10, 8)
		if err != nil || v == 0 {
			return nil, fmt.Errorf("secretbox: entry %d: version must be 1..255", i+1)
		}
		key, err := decodeKey(b64)
		if err != nil {
			return nil, fmt.Errorf("secretbox: entry %d: %w", i+1, err)
		}
		if _, dup := k.aeads[byte(v)]; dup {
			return nil, fmt.Errorf("secretbox: duplicate key version %d", v)
		}
		aead, err := newAEAD(key)
		if err != nil {
			return nil, err
		}
		k.aeads[byte(v)] = aead
		if i == 0 {
			k.current = byte(v)
		}
	}
	return k, nil
}

func decodeKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != KeySize {
				return nil, fmt.Errorf("key is %d bytes, want %d", len(b), KeySize)
			}
			return b, nil
		}
	}
	return nil, errors.New("key is not base64")
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return aead, nil
}

// Seal encrypts plaintext with the current key, binding it to aad.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	out := make([]byte, 1+nonceSize, 1+nonceSize+len(plaintext)+16)
	out[0] = k.current
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	return k.aeads[k.current].Seal(out, out[1:], plaintext, aad), nil
}

// Open decrypts a value from Seal. Any failure is ErrOpen.
func (k *Keyring) Open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < 1+nonceSize+16 {
		return nil, ErrOpen
	}
	aead, ok := k.aeads[sealed[0]]
	if !ok {
		return nil, ErrOpen
	}
	pt, err := aead.Open(nil, sealed[1:1+nonceSize], sealed[1+nonceSize:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// NeedsRotation reports whether sealed was not sealed with the current
// key, so a background job can re-seal it.
func (k *Keyring) NeedsRotation(sealed []byte) bool {
	return len(sealed) == 0 || sealed[0] != k.current
}

// String never reveals key material.
func (k *Keyring) String() string {
	return fmt.Sprintf("secretbox.Keyring{current: %d, keys: %d}", k.current, len(k.aeads))
}

// LogValue never reveals key material.
func (k *Keyring) LogValue() slog.Value { return slog.StringValue(k.String()) }
