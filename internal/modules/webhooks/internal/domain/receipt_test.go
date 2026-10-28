package domain_test

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
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

func TestValidChallenge(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"1158201444":             true,
		"abc-DEF_1.2~":           true,
		strings.Repeat("9", 200): true,
		"":                       false,
		strings.Repeat("9", 201): false,
		"<script>":               false,
		"a b":                    false,
		"1\r\nX: y":              false,
		"١٢٣":                    false,
	}
	for c, want := range tests {
		if got := domain.ValidChallenge(c); got != want {
			t.Errorf("ValidChallenge(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestPartValid(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	ok := domain.Part{Store: tenant.StoreID{ID: ids.New()}, ConnectionID: ids.New(), EventID: "wamid.1", Body: []byte(`{}`)}
	if !ok.Valid() {
		t.Fatal("valid part refused")
	}
	tests := map[string]func(p *domain.Part){
		"no store":      func(p *domain.Part) { p.Store = tenant.StoreID{} },
		"no connection": func(p *domain.Part) { p.ConnectionID = ulid.ID{} },
		"bad event id":  func(p *domain.Part) { p.EventID = "a b" },
		"empty body":    func(p *domain.Part) { p.Body = nil },
		"big body":      func(p *domain.Part) { p.Body = make([]byte, domain.MaxBody+1) },
	}
	for name, change := range tests {
		p := ok
		change(&p)
		if p.Valid() {
			t.Errorf("%s: accepted", name)
		}
	}
}

// FuzzValidChallenge: whatever a handshake carries, an accepted
// challenge is safe to echo: 1-200 URL-safe ASCII characters.
func FuzzValidChallenge(f *testing.F) {
	for _, s := range []string{"1158201444", "", "<script>", "a\r\nb", strings.Repeat("9", 201), "١٢٣"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !domain.ValidChallenge(s) {
			return
		}
		if len(s) == 0 || len(s) > domain.MaxChallenge {
			t.Fatalf("accepted length %d", len(s))
		}
		for _, r := range s {
			safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._~-", r)
			if !safe {
				t.Fatalf("accepted %q", r)
			}
		}
	})
}
