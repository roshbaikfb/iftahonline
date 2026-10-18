// Package password hashes and verifies passwords with argon2id (ADR-0005),
// in the PHC string format so parameters can be raised later and old
// hashes upgraded on the next successful login.
//
// argon2id is deliberately memory-hard: each hash holds Params.MemoryKiB
// for its duration. A semaphore bounds concurrent hashes so a login flood
// cannot push the process past GOMEMLIMIT; callers that cannot get a slot
// before their deadline get errs.Unavailable (503), not an OOM kill.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Params are the argon2id cost parameters.
type Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

// DefaultParams is OWASP's minimum recommendation (m=19 MiB, t=2, p=1).
func DefaultParams() Params { return Params{MemoryKiB: 19 * 1024, Time: 2, Threads: 1} }

const (
	saltLen = 16
	keyLen  = 32
)

// ErrMalformed means a stored hash cannot be parsed.
var ErrMalformed = errors.New("password: malformed hash")

// Hasher hashes with fixed parameters and a bounded number of concurrent
// computations.
type Hasher struct {
	p     Params
	sem   chan struct{}
	dummy string // a real hash of a random password, for unknown accounts
}

// New validates p and returns a hasher allowing maxConcurrent hashes at
// once.
func New(p Params, maxConcurrent int) (*Hasher, error) {
	if p.MemoryKiB < 8*1024 || p.MemoryKiB > 1024*1024 || p.Time < 1 || p.Time > 10 || p.Threads < 1 || maxConcurrent < 1 {
		return nil, fmt.Errorf("password: invalid parameters %+v, concurrency %d", p, maxConcurrent)
	}
	h := &Hasher{p: p, sem: make(chan struct{}, maxConcurrent)}
	junk := make([]byte, 24)
	if _, err := rand.Read(junk); err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	dummy, err := h.Hash(context.Background(), base64.RawStdEncoding.EncodeToString(junk))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Unavailable(2*time.Second, fmt.Errorf("password: no hashing slot: %w", err))
	}
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, errs.Unavailable(2*time.Second, fmt.Errorf("password: no hashing slot: %w", ctx.Err()))
	}
}

// Hash returns the PHC encoding of a new salted hash of password.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.p.Time, h.p.MemoryKiB, h.p.Threads, keyLen)
	release()
	return encode(h.p, salt, key), nil
}

// Verify reports whether password matches encoded, and whether encoded
// uses weaker parameters than the hasher's (so the caller should store a
// fresh hash).
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (ok, rehash bool, err error) {
	p, salt, want, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want))) //nolint:gosec // len(want) <= 64 by decode.
	release()
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, p != h.p, nil
}

// VerifyDummy spends the same work as a real verification and always
// fails. Login calls it for unknown accounts so timing does not reveal
// which emails are registered.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, _, err := h.Verify(ctx, password, h.dummy)
	return err
}

func encode(p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// decode parses "$argon2id$v=19$m=..,t=..,p=..$salt$key". Parameters are
// bounded so a tampered row cannot make verification allocate gigabytes.
func decode(s string) (Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, ErrMalformed
	}
	var p Params
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return Params{}, nil, nil, ErrMalformed
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return Params{}, nil, nil, ErrMalformed
		}
		switch k {
		case "m":
			p.MemoryKiB = uint32(n)
		case "t":
			p.Time = uint32(n)
		case "p":
			if n > 255 {
				return Params{}, nil, nil, ErrMalformed
			}
			p.Threads = uint8(n)
		default:
			return Params{}, nil, nil, ErrMalformed
		}
	}
	if p.MemoryKiB < 1024 || p.MemoryKiB > 1024*1024 || p.Time < 1 || p.Time > 10 || p.Threads < 1 || p.Threads > 16 {
		return Params{}, nil, nil, ErrMalformed
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return Params{}, nil, nil, ErrMalformed
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return Params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}
