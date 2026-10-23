package domain_test

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestValidEventID(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"evt_1":                  true,
		"txn:123:refund":         true,
		strings.Repeat("a", 200): true,
		"":                       false,
		strings.Repeat("a", 201): false,
		"has space":              false,
		"tab\there":              false,
		"nl\n":                   false,
		"\x00":                   false,
		"bad\xffutf8":            false,
		"عربي":                   true,
	}
	for id, want := range tests {
		if got := domain.ValidEventID(id); got != want {
			t.Errorf("ValidEventID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestValidNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{"bosta": true, "paymob": true, "a1_b": true, "x": false, "Bosta": false, "1abc": false, "has-dash": false} {
		if got := domain.ValidProvider(name); got != want {
			t.Errorf("ValidProvider(%q) = %v", name, got)
		}
	}
	for o, want := range map[string]bool{"paid": true, "stale_event": true, "": false, "Paid": false, "x1": false} {
		if got := domain.ValidOutcome(o); got != want {
			t.Errorf("ValidOutcome(%q) = %v", o, got)
		}
	}
}

func TestReceivedAtOf(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	id := ulid.NewGeneratorWith(func() time.Time { return at }, rand.Reader).New()
	if got := domain.ReceivedAtOf(id); !got.Equal(at) || got.Location() != time.UTC {
		t.Fatalf("ReceivedAtOf = %v", got)
	}
}

func FuzzValidEventID(f *testing.F) {
	f.Add("evt_1")
	f.Add("")
	f.Add("a b")
	f.Fuzz(func(t *testing.T, id string) {
		if domain.ValidEventID(id) && (len(id) > domain.MaxEventID || strings.ContainsAny(id, " \t\r\n\x00")) {
			t.Fatalf("accepted %q", id)
		}
	})
}
