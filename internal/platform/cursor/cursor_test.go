package cursor_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 24, 10, 30, 0, 123456000, time.UTC)
	id := ulid.NewGenerator().New()
	c := cursor.Encode(at, id)
	gotAt, gotID, err := cursor.Decode(c)
	if err != nil || !gotAt.Equal(at) || gotID != id || gotAt.Location() != time.UTC {
		t.Fatalf("Decode(Encode) = %v %v %v", gotAt, gotID, err)
	}
	// Microseconds are kept, nanoseconds dropped (as Postgres keeps time).
	if a, _, _ := cursor.Decode(cursor.Encode(at.Add(999), id)); !a.Equal(at) {
		t.Fatalf("nanoseconds kept: %v", a)
	}
}

func TestDecodeRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"", "nope", "!!!", "MTIz", "MTIzOm5vcGU", "eDox", cursor.Encode(time.Time{}, ulid.ID{})[:3]} {
		if _, _, err := cursor.Decode(c); err == nil {
			t.Errorf("Decode(%q) accepted", c)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(cursor.Encode(time.Now(), ulid.NewGenerator().New()))
	f.Add("MTIzOjAx")
	f.Fuzz(func(t *testing.T, c string) {
		at, id, err := cursor.Decode(c)
		if err != nil {
			return
		}
		// What decodes encodes back to a cursor that decodes the same.
		at2, id2, err := cursor.Decode(cursor.Encode(at, id))
		if err != nil || !at2.Equal(at) || id2 != id {
			t.Fatalf("%q: %v %v, then %v %v %v", c, at, id, at2, id2, err)
		}
	})
}
