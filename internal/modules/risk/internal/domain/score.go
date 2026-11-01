// Package domain holds the risk module's rules (ADR-0065): what an
// order's end teaches about its customer's phone, and how a phone's
// outcomes across the platform become a band. It imports only the
// standard library and internal/platform (CLAUDE.md rule 3).
//
// Scoring is deterministic: integer arithmetic on the signals and the
// time given, whatever order the signals come in.
package domain

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// Outcome is how an order ended for its customer.
type Outcome string

// Outcomes.
const (
	Delivered   Outcome = "delivered"   // the customer took and paid for it
	Returned    Outcome = "returned"    // the parcel came back
	Refused     Outcome = "refused"     // the customer refused the parcel or the order
	NoAnswer    Outcome = "no_answer"   // the customer did not answer the courier or the store
	Unconfirmed Outcome = "unconfirmed" // the customer left the confirmation message unanswered
)

// Outcomes lists every outcome.
func Outcomes() []Outcome { return []Outcome{Delivered, Returned, Refused, NoAnswer, Unconfirmed} }

// OutcomeOf is what an order in status, with the reason of its latest
// move, teaches (the orders module's statuses and reasons, ADR-0065);
// ok is false while it teaches nothing: still on its way, or cancelled
// for no reason that is the customer's.
func OutcomeOf(status, reason string) (o Outcome, ok bool) {
	switch status {
	case "delivered":
		return Delivered, true
	case "returned":
		if reason == "refused" {
			return Refused, true
		}
		return Returned, true
	case "failed_delivery":
		if reason == "refused" {
			return Refused, true
		}
		return NoAnswer, true // a courier's failed attempt is nearly always an unanswered phone
	case "cancelled":
		switch reason {
		case "refused":
			return Refused, true
		case "no_answer":
			return NoAnswer, true
		case "unconfirmed":
			return Unconfirmed, true
		}
	}
	return "", false
}

// badness is how much of a failure each outcome is, in thousandths:
// delivered is the only success.
func badness(o Outcome) int64 {
	switch o {
	case Returned, Refused:
		return 1000
	case NoAnswer:
		return 600
	case Unconfirmed:
		return 300
	case Delivered:
	}
	return 0
}

// Band is what merchants see of a phone's score.
type Band string

// Bands.
const (
	BandNew    Band = "new"    // too little history to say
	BandLow    Band = "low"    // usually takes its orders
	BandMedium Band = "medium" // often does not
	BandHigh   Band = "high"   // mostly does not, in more than one store
)

// Bands lists every band, from no history to the riskiest.
func Bands() []Band { return []Band{BandNew, BandLow, BandMedium, BandHigh} }

// AtLeast reports whether b is as risky as floor or more; BandNew is
// below every other band.
func (b Band) AtLeast(floor Band) bool {
	return slices.Index(Bands(), b) >= slices.Index(Bands(), floor)
}

// Signal is one order's outcome for a phone, as the platform-wide lookup
// returns it: never the store or the order, only an opaque number that
// is the same for the phone's outcomes in one store.
type Signal struct {
	Outcome Outcome
	At      time.Time // when the order reached its outcome
	Store   int
}

// The model (ADR-0065). Weights are in millionths of a fresh outcome.
const (
	HalfLife       = 90 * 24 * time.Hour  // an outcome's weight halves every 90 days
	Horizon        = 730 * 24 * time.Hour // older outcomes count for nothing (and are purged)
	PerStore       = 3                    // one store's most recent outcomes that count
	MaxSignals     = 200                  // the most outcomes a lookup returns per phone
	unit           = 1_000_000
	priorBad       = 250_000 // a phone starts as 1 success and a quarter of a failure: 20%
	priorGood      = 1_000_000
	minEvidence    = 500_000 // less weight than half a fresh outcome is no history
	mediumFrom     = 300     // thousandths of failure
	highFrom       = 550
	highNeedStores = 2 // failures from one store alone never make a phone high
)

// Assessment is a phone's score.
type Assessment struct {
	Band Band
	// Score is the weighted share of failures, in thousandths, with the
	// prior; 200 for a phone with no history.
	Score int
	// Evidence is the outcomes' total weight, in millionths of a fresh
	// outcome.
	Evidence int64
}

// Assess scores a phone from its signals at now. Each outcome weighs
// 2^(-age/HalfLife); only each store's PerStore most recent count, so no
// single store can sink a phone, and a high band needs failures from at
// least two stores.
func Assess(signals []Signal, now time.Time) Assessment {
	s := slices.Clone(signals)
	slices.SortFunc(s, func(a, b Signal) int {
		return cmp.Or(b.At.Compare(a.At), cmp.Compare(a.Store, b.Store), cmp.Compare(a.Outcome, b.Outcome))
	})
	perStore := map[int]int{}
	failedStores := map[int]bool{}
	var good, bad int64
	for _, sig := range s {
		age := now.Sub(sig.At)
		if age >= Horizon || perStore[sig.Store] == PerStore {
			continue
		}
		perStore[sig.Store]++
		w := weight(age)
		if b := badness(sig.Outcome); b > 0 {
			bad += w * b / 1000
			failedStores[sig.Store] = true
		} else {
			good += w
		}
	}
	a := Assessment{Evidence: good + bad}
	a.Score = int((bad + priorBad) * 1000 / (good + bad + priorBad + priorGood))
	switch {
	case a.Evidence < minEvidence:
		a.Band = BandNew
	case a.Score < mediumFrom:
		a.Band = BandLow
	case a.Score < highFrom || len(failedStores) < highNeedStores:
		a.Band = BandMedium
	default:
		a.Band = BandHigh
	}
	return a
}

// weight is 2^(-age/HalfLife) in millionths, by whole days (an outcome
// in the future weighs as one today). Rounding to millionths makes it
// the same on every platform.
func weight(age time.Duration) int64 {
	days := max(int64(age/(24*time.Hour)), 0)
	return int64(math.Round(unit * math.Exp2(-float64(days)/float64(HalfLife/(24*time.Hour)))))
}

// History is a phone's orders in one store: what merchants see of their
// own customers next to the band.
type History struct {
	Orders   int // every order, with an outcome or not yet
	Outcomes map[Outcome]int
	LastAt   time.Time // the latest outcome; zero without one
}
