package domain_test

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
)

// now is the fixtures' clock: signals are dated in days before it.
var now = time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC) //nolint:gochecknoglobals // the fixtures' fixed clock

type fixture struct {
	Name    string `json:"name"`
	Signals []struct {
		Outcome domain.Outcome `json:"outcome"`
		DaysAgo int            `json:"days_ago"`
		Store   int            `json:"store"`
	} `json:"signals"`
	Want struct {
		Band     domain.Band `json:"band"`
		Score    int         `json:"score"`
		Evidence int64       `json:"evidence"`
	} `json:"want"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/assess.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []fixture
	if err = json.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	if len(fs) < 10 {
		t.Fatalf("only %d fixtures", len(fs))
	}
	return fs
}

func (f fixture) signals() []domain.Signal {
	out := make([]domain.Signal, len(f.Signals))
	for i, s := range f.Signals {
		out[i] = domain.Signal{Outcome: s.Outcome, At: now.Add(-time.Duration(s.DaysAgo) * 24 * time.Hour), Store: s.Store}
	}
	return out
}

// TestAssessFixtures is the P8 acceptance check that scoring is
// deterministic: every fixture's band, score and evidence, exactly.
func TestAssessFixtures(t *testing.T) {
	t.Parallel()
	for _, f := range loadFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			got := domain.Assess(f.signals(), now)
			if got.Band != f.Want.Band || got.Score != f.Want.Score || got.Evidence != f.Want.Evidence {
				t.Fatalf("got %+v, want %+v", got, f.Want)
			}
		})
	}
}

// TestAssessIgnoresOrder: the signals' order never changes a score, and
// the input is not modified.
func TestAssessIgnoresOrder(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a fixed shuffle, not a secret
	for _, f := range loadFixtures(t) {
		s := f.signals()
		want := domain.Assess(s, now)
		for range 20 {
			r.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
			before := append([]domain.Signal(nil), s...)
			if got := domain.Assess(s, now); got != want {
				t.Fatalf("%s: shuffled %+v, want %+v", f.Name, got, want)
			}
			for i := range s {
				if s[i] != before[i] {
					t.Fatalf("%s: Assess reordered its input", f.Name)
				}
			}
		}
	}
}

// TestAssessAges: the same outcomes weigh less as time passes, and a
// phone's band can only fall back towards new as its failures age.
func TestAssessAges(t *testing.T) {
	t.Parallel()
	s := []domain.Signal{
		{Outcome: domain.Refused, At: now, Store: 1},
		{Outcome: domain.Returned, At: now, Store: 2},
	}
	prev := domain.Assess(s, now)
	if prev.Band != domain.BandHigh {
		t.Fatalf("fresh: %+v", prev)
	}
	for _, days := range []int{30, 90, 180, 365, 729, 730} {
		got := domain.Assess(s, now.Add(time.Duration(days)*24*time.Hour))
		if got.Evidence >= prev.Evidence {
			t.Fatalf("after %d days the evidence did not fall: %+v then %+v", days, prev, got)
		}
		prev = got
	}
	if prev.Band != domain.BandNew || prev.Evidence != 0 {
		t.Fatalf("past the horizon: %+v", prev)
	}
	// 90 days halve a weight.
	if half := domain.Assess(s[:1], now.Add(domain.HalfLife)); half.Evidence != 500_000 {
		t.Fatalf("after one half-life: %+v", half)
	}
}

func TestOutcomeOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status, reason string
		want           domain.Outcome
		ok             bool
	}{
		{"delivered", "", domain.Delivered, true},
		{"returned", "", domain.Returned, true},
		{"returned", "refused", domain.Refused, true},
		{"returned", "no_answer", domain.Returned, true},
		{"failed_delivery", "", domain.NoAnswer, true},
		{"failed_delivery", "no_answer", domain.NoAnswer, true},
		{"failed_delivery", "refused", domain.Refused, true},
		{"cancelled", "refused", domain.Refused, true},
		{"cancelled", "no_answer", domain.NoAnswer, true},
		{"cancelled", "unconfirmed", domain.Unconfirmed, true},
		{"cancelled", "", "", false}, // the merchant's or the customer's own choice
		{"pending", "", "", false},
		{"confirmed", "", "", false},
		{"ready_to_ship", "", "", false},
		{"shipped", "", "", false},
		{"unknown", "refused", "", false},
	}
	for _, tt := range tests {
		if got, ok := domain.OutcomeOf(tt.status, tt.reason); got != tt.want || ok != tt.ok {
			t.Errorf("%s/%q = %q %v, want %q %v", tt.status, tt.reason, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBandAtLeast(t *testing.T) {
	t.Parallel()
	bands := domain.Bands()
	for i, b := range bands {
		for j, min := range bands {
			if got := b.AtLeast(min); got != (i >= j) {
				t.Errorf("%s.AtLeast(%s) = %v", b, min, got)
			}
		}
	}
	if domain.Band("bogus").AtLeast(domain.BandNew) {
		t.Error("an unknown band is below every band")
	}
}
