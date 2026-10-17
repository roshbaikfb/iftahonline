// Package ulid provides the platform's public identifier type (CLAUDE.md
// rule 6). IDs are 128-bit ULIDs: time-ordered, unguessable and safe to
// expose. They are stored in PostgreSQL as uuid (16 bytes) and rendered
// publicly as 26-character Crockford base32.
package ulid

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	oklog "github.com/oklog/ulid/v2"
)

// ErrInvalid is returned for malformed IDs.
var ErrInvalid = errors.New("ulid: invalid id")

// ID is a ULID. The zero value is the nil ID; check IsZero.
type ID [16]byte

// Generator produces strictly increasing IDs, even within one millisecond.
// It is safe for concurrent use. Create one per process and inject it.
type Generator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy *oklog.MonotonicEntropy
}

// NewGenerator returns a Generator using crypto/rand entropy and the
// system clock.
func NewGenerator() *Generator {
	return NewGeneratorWith(time.Now, rand.Reader)
}

// NewGeneratorWith lets tests control the clock and entropy source.
func NewGeneratorWith(now func() time.Time, entropy io.Reader) *Generator {
	return &Generator{now: now, entropy: oklog.Monotonic(entropy, 0)}
}

// New returns a fresh ID. It does not fail: if the monotonic counter for
// the current millisecond is exhausted (2^80 IDs), it waits for the next.
func (g *Generator) New() ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		u, err := oklog.New(oklog.Timestamp(g.now()), g.entropy)
		if err == nil {
			return ID(u)
		}
		time.Sleep(time.Millisecond)
	}
}

// Parse decodes a 26-character Crockford base32 ULID (case-insensitive).
func Parse(s string) (ID, error) {
	u, err := oklog.ParseStrict(s)
	if err != nil {
		return ID{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return ID(u), nil
}

// String returns the canonical 26-character upper-case form.
func (id ID) String() string { return oklog.ULID(id).String() }

// IsZero reports whether id is the nil ID.
func (id ID) IsZero() bool { return id == ID{} }

// Time returns the millisecond timestamp embedded in id.
func (id ID) Time() time.Time { return oklog.Time(oklog.ULID(id).Time()) }

// MarshalText implements encoding.TextMarshaler (used by JSON).
func (id ID) MarshalText() ([]byte, error) {
	return oklog.ULID(id).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (id *ID) UnmarshalText(b []byte) error {
	v, err := Parse(string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// Value implements driver.Valuer, encoding the ID as a uuid string so it
// binds to a PostgreSQL uuid column. The nil ID is stored as NULL.
func (id ID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return id.uuidString(), nil
}

// Scan implements sql.Scanner for uuid columns (string or 16 raw bytes).
func (id *ID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*id = ID{}
		return nil
	case string:
		return id.scanUUIDString(v)
	case []byte:
		if len(v) == len(id) {
			copy(id[:], v)
			return nil
		}
		return id.scanUUIDString(string(v))
	case [16]byte:
		*id = v
		return nil
	default:
		return fmt.Errorf("%w: cannot scan %T", ErrInvalid, src)
	}
}

func (id ID) uuidString() string {
	var buf [36]byte
	hex.Encode(buf[0:8], id[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], id[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], id[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], id[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:], id[10:])
	return string(buf[:])
}

func (id *ID) scanUUIDString(s string) error {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return fmt.Errorf("%w: bad uuid %q", ErrInvalid, s)
	}
	var out ID
	segs := [...][2]int{{0, 8}, {9, 13}, {14, 18}, {19, 23}, {24, 36}}
	pos := 0
	for _, sg := range segs {
		n, err := hex.Decode(out[pos:], []byte(s[sg[0]:sg[1]]))
		if err != nil {
			return fmt.Errorf("%w: bad uuid %q: %w", ErrInvalid, s, err)
		}
		pos += n
	}
	*id = out
	return nil
}
