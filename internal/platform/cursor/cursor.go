// Package cursor is the keyset cursor of lists ordered by (time, ID): an
// opaque token clients send back to continue a list. Every module's
// "next_cursor" uses it.
package cursor

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrInvalid is a cursor this package did not write.
var ErrInvalid = errors.New("cursor: invalid")

// maxLen bounds what Decode reads (a cursor is about 60 bytes).
const maxLen = 128

// Encode is the cursor after the item at (at, id); at is kept to the
// microsecond, as Postgres keeps it.
func Encode(at time.Time, id ulid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + ":" + id.String()))
}

// Decode reads a cursor written by Encode; anything else is ErrInvalid.
func Decode(c string) (time.Time, ulid.ID, error) {
	if c == "" || len(c) > maxLen {
		return time.Time{}, ulid.ID{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, ulid.ID{}, ErrInvalid
	}
	us, ids, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, ulid.ID{}, ErrInvalid
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return time.Time{}, ulid.ID{}, ErrInvalid
	}
	id, err := ulid.Parse(ids)
	if err != nil {
		return time.Time{}, ulid.ID{}, ErrInvalid
	}
	return time.UnixMicro(n).UTC(), id, nil
}
