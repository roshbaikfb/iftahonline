// Package envelope encrypts merchant credentials at rest with envelope
// encryption (ADR-0024): every secret gets its own random data key (DEK),
// the secret is sealed with the DEK (AES-256-GCM), and the DEK is sealed
// ("wrapped") by a key-encryption key (KEK) that never leaves its holder:
// the platform keyring from the environment today, a KMS later, behind
// the same KEK interface.
//
// Rotating the KEK rewraps the small DEKs (Rewrap) and never touches the
// secrets themselves. A copied blob does not open elsewhere: the caller's
// associated data (AAD), e.g. "payments:paymob:<store>:<connection>", is
// bound to both the secret and its wrapped DEK.
//
// Blob format (version 1):
//
//	'E' 0x01 | kek id length (1) | kek id | wrapped length (2, big endian)
//	| wrapped DEK | nonce (12) | ciphertext+tag
package envelope

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

// MaxPlaintext bounds what Seal accepts: credentials, not documents.
const MaxPlaintext = 64 << 10

const (
	magic     = 'E'
	version   = 1
	dekSize   = 32
	nonceSize = 12
	tagSize   = 16
)

// Errors. Match with errors.Is.
var (
	// ErrOpen means a blob is malformed, names an unknown KEK, or fails
	// authentication (tampered, or another AAD).
	ErrOpen = errors.New("envelope: cannot open")
	// ErrTooLarge means the plaintext exceeds MaxPlaintext.
	ErrTooLarge = errors.New("envelope: plaintext too large")
)

// KEK wraps and unwraps data keys. Implementations must be safe for
// concurrent use; a KMS-backed one makes a network call per Wrap and
// Unwrap and must honour ctx.
type KEK interface {
	// ID names the key holder in blobs, 1-64 bytes, stable across
	// restarts (e.g. "env", "kms:alias/merchant-creds").
	ID() string
	Wrap(ctx context.Context, dek, aad []byte) ([]byte, error)
	Unwrap(ctx context.Context, wrapped, aad []byte) ([]byte, error)
	// Current reports whether wrapped was made with the holder's current
	// key version; when not, Rewrap brings it up to date.
	Current(wrapped []byte) bool
}

// Sealer seals with one current KEK and opens with any known KEK.
type Sealer struct {
	current KEK
	byID    map[string]KEK
}

// New returns a Sealer that wraps with current and also opens blobs of
// the older KEKs (a move from one key holder to another).
func New(current KEK, older ...KEK) (*Sealer, error) {
	if current == nil {
		return nil, errors.New("envelope: no current KEK")
	}
	s := &Sealer{current: current, byID: map[string]KEK{}}
	for _, k := range append([]KEK{current}, older...) {
		id := k.ID()
		if id == "" || len(id) > 64 {
			return nil, fmt.Errorf("envelope: KEK id %q must be 1-64 bytes", id)
		}
		if _, dup := s.byID[id]; dup {
			return nil, fmt.Errorf("envelope: duplicate KEK id %q", id)
		}
		s.byID[id] = k
	}
	return s, nil
}

// Seal encrypts plaintext under a fresh DEK wrapped by the current KEK.
func (s *Sealer) Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrTooLarge
	}
	dek := make([]byte, dekSize)
	defer clear(dek)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("envelope: data key: %w", err)
	}
	wrapped, err := s.current.Wrap(ctx, dek, wrapAAD(aad))
	if err != nil {
		return nil, fmt.Errorf("envelope: wrap: %w", err)
	}
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err = rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("envelope: nonce: %w", err)
	}
	out, err := header(s.current.ID(), wrapped, nonce, len(plaintext)+tagSize)
	if err != nil {
		return nil, err
	}
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// Open decrypts a blob from Seal. Any failure to authenticate is ErrOpen;
// a KEK holder that cannot be reached returns its own error.
func (s *Sealer) Open(ctx context.Context, blob, aad []byte) ([]byte, error) {
	p, err := parse(blob)
	if err != nil {
		return nil, err
	}
	kek, ok := s.byID[p.kekID]
	if !ok {
		return nil, ErrOpen
	}
	dek, err := kek.Unwrap(ctx, p.wrapped, wrapAAD(aad))
	if err != nil {
		return nil, unwrapErr(err)
	}
	defer clear(dek)
	if len(dek) != dekSize {
		return nil, ErrOpen
	}
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	pt, err := aead.Open(nil, p.nonce, p.sealed, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// NeedsRewrap reports whether blob's DEK is not wrapped by the current
// KEK at its current version. A malformed blob needs it too (Rewrap will
// then fail and report it).
func (s *Sealer) NeedsRewrap(blob []byte) bool {
	p, err := parse(blob)
	if err != nil {
		return true
	}
	return p.kekID != s.current.ID() || !s.current.Current(p.wrapped)
}

// Rewrap unwraps blob's DEK and wraps it again with the current KEK. The
// sealed secret is copied unchanged.
func (s *Sealer) Rewrap(ctx context.Context, blob, aad []byte) ([]byte, error) {
	p, err := parse(blob)
	if err != nil {
		return nil, err
	}
	kek, ok := s.byID[p.kekID]
	if !ok {
		return nil, ErrOpen
	}
	dek, err := kek.Unwrap(ctx, p.wrapped, wrapAAD(aad))
	if err != nil {
		return nil, unwrapErr(err)
	}
	defer clear(dek)
	// Prove the DEK opens the secret before writing a new blob for it.
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	if _, err = aead.Open(nil, p.nonce, p.sealed, aad); err != nil {
		return nil, ErrOpen
	}
	wrapped, err := s.current.Wrap(ctx, dek, wrapAAD(aad))
	if err != nil {
		return nil, fmt.Errorf("envelope: wrap: %w", err)
	}
	out, err := header(s.current.ID(), wrapped, p.nonce, len(p.sealed))
	if err != nil {
		return nil, err
	}
	return append(out, p.sealed...), nil
}

// header is a blob up to and including the nonce, with room for rest
// more bytes.
func header(kekID string, wrapped, nonce []byte, rest int) ([]byte, error) {
	if len(wrapped) == 0 || len(wrapped) > 0xffff {
		return nil, errors.New("envelope: wrapped key size out of range")
	}
	out := make([]byte, 0, 5+len(kekID)+len(wrapped)+len(nonce)+rest)
	out = append(out, magic, version, byte(len(kekID))) //nolint:gosec // New checks ids are 1-64 bytes
	out = append(out, kekID...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(wrapped))) //nolint:gosec // bounded above
	out = append(out, wrapped...)
	return append(out, nonce...), nil
}

type parts struct {
	kekID   string
	wrapped []byte
	nonce   []byte
	sealed  []byte
}

func parse(b []byte) (parts, error) {
	if len(b) < 3 || b[0] != magic || b[1] != version {
		return parts{}, ErrOpen
	}
	n := int(b[2])
	b = b[3:]
	if n == 0 || len(b) < n+2 {
		return parts{}, ErrOpen
	}
	p := parts{kekID: string(b[:n])}
	b = b[n:]
	w := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if w == 0 || len(b) < w+nonceSize+tagSize {
		return parts{}, ErrOpen
	}
	p.wrapped, b = b[:w], b[w:]
	p.nonce, p.sealed = b[:nonceSize], b[nonceSize:]
	return p, nil
}

// wrapAAD binds the wrapped DEK to the same context as the secret, so a
// DEK cannot be lifted from one blob into another.
func wrapAAD(aad []byte) []byte { return append([]byte("envelope-dek:"), aad...) }

func unwrapErr(err error) error {
	if errors.Is(err, secretbox.ErrOpen) {
		return ErrOpen
	}
	return fmt.Errorf("envelope: unwrap: %w", err)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	return aead, nil
}

// KeyringKEK is the KEK held in the environment: the platform keyring
// (SECRETBOX_KEYS). Its key versions rotate like every secretbox key.
type KeyringKEK struct{ k *secretbox.Keyring }

// FromKeyring returns the environment KEK.
func FromKeyring(k *secretbox.Keyring) KeyringKEK { return KeyringKEK{k: k} }

// ID implements KEK.
func (KeyringKEK) ID() string { return "env" }

// Wrap implements KEK.
func (e KeyringKEK) Wrap(_ context.Context, dek, aad []byte) ([]byte, error) {
	return e.k.Seal(dek, aad)
}

// Unwrap implements KEK.
func (e KeyringKEK) Unwrap(_ context.Context, wrapped, aad []byte) ([]byte, error) {
	return e.k.Open(wrapped, aad)
}

// Current implements KEK.
func (e KeyringKEK) Current(wrapped []byte) bool { return !e.k.NeedsRotation(wrapped) }
