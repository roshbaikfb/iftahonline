// Package domain holds the rules of per-store quotas (ADR-0038): which
// features are metered, the calendar month they are counted in, and the
// warning thresholds a merchant hears about.
package domain

import (
	"errors"
	"time"
)

// Feature is a metered capability.
type Feature string

// Metered features.
const (
	// Messages are customer messages sent on the store's behalf; a
	// WhatsApp message or an SMS is one unit each.
	Messages Feature = "messages"
)

// Valid reports whether f is a metered feature.
func (f Feature) Valid() bool { return f == Messages }

// Threshold is a share of the limit a merchant is warned at.
type Threshold int

// Warning thresholds, in percent of the limit.
const (
	NoThreshold Threshold = 0
	Nearly      Threshold = 80
	Reached     Threshold = 100
)

// MaxUnits bounds a single consumption or release.
const MaxUnits = 1000

// Errors.
var (
	// ErrQuotaExceeded means the units would pass the store's limit for
	// the month; nothing was counted.
	ErrQuotaExceeded = errors.New("entitlements: quota exceeded")
	// ErrInvalid is an unknown feature, a unit count outside 1..MaxUnits
	// or a period that is not a month's first day.
	ErrInvalid = errors.New("entitlements: invalid request")
)

// PeriodOf is the month t falls in: its first day, 00:00 UTC.
func PeriodOf(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// ValidPeriod reports whether p is a month's first day, 00:00 UTC.
func ValidPeriod(p time.Time) bool { return !p.IsZero() && p.Equal(PeriodOf(p)) }

// Usage is a store's count for a feature in one month.
type Usage struct {
	Feature Feature
	Period  time.Time
	Used    int64
	Limit   int64
}

// Remaining is what the store may still use this month.
func (u Usage) Remaining() int64 { return max(u.Limit-u.Used, 0) }

// Level is the highest threshold u has reached.
func (u Usage) Level() Threshold {
	switch {
	case u.Limit <= 0 || u.Used >= u.Limit:
		return Reached
	case u.Used*100 >= u.Limit*int64(Nearly):
		return Nearly
	default:
		return NoThreshold
	}
}

// CrossedBy is the threshold that consuming the last n units crossed
// (NoThreshold when u was already at that level before them), so the
// merchant is warned once per threshold and month.
func (u Usage) CrossedBy(n int64) Threshold {
	before := u
	before.Used = max(u.Used-n, 0)
	if lvl := u.Level(); lvl != before.Level() {
		return lvl
	}
	return NoThreshold
}
