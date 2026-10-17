package ulid_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestGeneratorMonotonic(t *testing.T) {
	t.Parallel()
	fixed := time.UnixMilli(1_700_000_000_000)
	g := ulid.NewGeneratorWith(func() time.Time { return fixed }, bytes.NewReader(bytes.Repeat([]byte{7}, 1<<16)))
	prev := g.New()
	for range 1000 {
		next := g.New()
		if next.String() <= prev.String() {
			t.Fatalf("not increasing: %s then %s", prev, next)
		}
		if !next.Time().Equal(fixed) {
			t.Fatalf("time = %v, want %v", next.Time(), fixed)
		}
		prev = next
	}
}

func TestGeneratorConcurrentUnique(t *testing.T) {
	t.Parallel()
	g := ulid.NewGenerator()
	const workers, per = 8, 500
	var mu sync.Mutex
	seen := make(map[ulid.ID]struct{}, workers*per)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			local := make([]ulid.ID, 0, per)
			for range per {
				local = append(local, g.New())
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range local {
				seen[id] = struct{}{}
			}
		})
	}
	wg.Wait()
	if len(seen) != workers*per {
		t.Fatalf("got %d unique IDs, want %d", len(seen), workers*per)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"01ARZ3NDEKTSV4RRFFQ69G5FAV", false},
		{"01arz3ndektsv4rrffq69g5fav", false},
		{"", true},
		{"01ARZ3NDEKTSV4RRFFQ69G5FA", true},   // 25 chars
		{"01ARZ3NDEKTSV4RRFFQ69G5FAVX", true}, // 27 chars
		{"01ARZ3NDEKTSV4RRFFQ69G5FAU", true},  // U is not Crockford
		{"81ARZ3NDEKTSV4RRFFQ69G5FAV", true},  // timestamp overflow
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			id, err := ulid.Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ulid.ErrInvalid) {
				t.Fatalf("err %v is not ErrInvalid", err)
			}
			if err == nil && id.String() != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
				t.Fatalf("String = %s", id)
			}
		})
	}
}

func TestJSON(t *testing.T) {
	t.Parallel()
	id := ulid.NewGenerator().New()
	b, err := json.Marshal(struct{ ID ulid.ID }{id})
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ ID ulid.ID }
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != id {
		t.Fatalf("round trip %s != %s", back.ID, id)
	}
	if err := json.Unmarshal([]byte(`{"ID":"nope"}`), &back); err == nil {
		t.Fatal("expected error for bad id")
	}
}

func TestValueScan(t *testing.T) {
	t.Parallel()
	id, _ := ulid.Parse("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	v, err := id.Value()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok || s != "01563e3a-b5d3-d676-4c61-efb99302bd5b" {
		t.Fatalf("Value = %#v", v)
	}
	for _, src := range []any{s, []byte(s), id[:], [16]byte(id)} {
		var got ulid.ID
		if err := got.Scan(src); err != nil || got != id {
			t.Fatalf("Scan(%T) = %s, %v", src, got, err)
		}
	}
	var zero ulid.ID
	if v, _ := zero.Value(); v != nil {
		t.Fatalf("zero Value = %#v, want nil", v)
	}
	got := id
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Fatalf("Scan(nil) = %s, %v", got, err)
	}
	for _, bad := range []any{"short", "01563e3a-b5d3-d676-4c61-efb99302bd5", "01563e3axb5d3-d676-4c61-efb99302bd5b", "zz563e3a-b5d3-d676-4c61-efb99302bd5b", 42} {
		if err := got.Scan(bad); !errors.Is(err, ulid.ErrInvalid) {
			t.Errorf("Scan(%v) err = %v", bad, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	f.Add("7ZZZZZZZZZZZZZZZZZZZZZZZZZ")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		id, err := ulid.Parse(s)
		if err != nil {
			return
		}
		back, err := ulid.Parse(id.String())
		if err != nil || back != id {
			t.Fatalf("round trip %q -> %s -> %s, %v", s, id, back, err)
		}
		var scanned ulid.ID
		v, _ := id.Value()
		if v != nil {
			if err := scanned.Scan(v); err != nil || scanned != id {
				t.Fatalf("uuid round trip %s: %v", id, err)
			}
		}
	})
}
