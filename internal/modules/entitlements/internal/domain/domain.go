// Package domain holds the rules of what a store may use (ADR-0038,
// ADR-0045): the features the code enforces and their kinds, what a
// store is granted of each, the calendar month metered features are
// counted in, and the warning thresholds a merchant hears about.
package domain

import (
	"errors"
	"math"
	"time"
)

// Feature is a capability a plan grants. The features below are the ones
// the code enforces; the console may add others to the catalog, which
// are shown on plans but enforced by nothing until code asks for them.
type Feature string

// Kind is how a feature is granted.
type Kind string

// Kinds.
const (
	Flag    Kind = "flag"    // on (1) or off (0)
	Count   Kind = "count"   // how many the store may hold
	Monthly Kind = "monthly" // units per calendar month, counted in usage_counters
)

// Enforced features. Each is a row of the features table (00056) of the
// same kind; a test holds the two together.
const (
	// Messages are customer messages sent on the store's behalf; a
	// WhatsApp message or an SMS is one unit each.
	Messages       Feature = "messages"
	OrdersPerMonth Feature = "orders_per_month"
	Products       Feature = "products"
	Staff          Feature = "staff"
	CustomDomains  Feature = "custom_domains"
	CardPayments   Feature = "card_payments"
	Coupons        Feature = "coupons"
	Pixels         Feature = "pixels"
	Feeds          Feature = "feeds"
	AdFree         Feature = "ad_free"
	// RiskRules lets a store hold or verify risky customers' orders
	// (ADR-0066); the bands themselves are free.
	RiskRules Feature = "risk_rules"
)

// Features lists the enforced features with their kinds.
func Features() map[Feature]Kind {
	all := [...]Feature{Messages, OrdersPerMonth, Products, Staff, CustomDomains, CardPayments, Coupons, Pixels, Feeds, AdFree, RiskRules}
	m := make(map[Feature]Kind, len(all))
	for _, f := range all {
		m[f] = f.Kind()
	}
	return m
}

// Kind is f's kind; "" when the code does not enforce f.
func (f Feature) Kind() Kind {
	switch f {
	case Messages, OrdersPerMonth:
		return Monthly
	case Products, Staff, CustomDomains:
		return Count
	case CardPayments, Coupons, Pixels, Feeds, AdFree, RiskRules:
		return Flag
	}
	return ""
}

// Valid reports whether f is a metered feature: one counted per month.
func (f Feature) Valid() bool { return f.Kind() == Monthly }

// Unlimited is the limit of a feature granted without one.
const Unlimited int64 = math.MaxInt64

// Grant is what a store is granted of one feature.
type Grant struct {
	Value     int64 // 0 or 1 for a flag; a count; units a month
	Unlimited bool
}

// Limit is the grant as a number: Unlimited when there is none.
func (g Grant) Limit() int64 {
	if g.Unlimited {
		return Unlimited
	}
	return g.Value
}

// Grants are a store's grants. A feature not listed is not granted.
type Grants map[Feature]Grant

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
	// ErrLimitReached means the store already holds what its plan
	// grants of a counted feature.
	ErrLimitReached = errors.New("entitlements: plan limit reached")
	// ErrInvalid is a feature unknown or of the wrong kind for the call,
	// a unit count outside 1..MaxUnits or a period that is not a month's
	// first day.
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

// Unlimited reports whether the feature has no monthly limit.
func (u Usage) Unlimited() bool { return u.Limit == Unlimited }

// Remaining is what the store may still use this month.
func (u Usage) Remaining() int64 { return max(u.Limit-u.Used, 0) }

// Level is the highest threshold u has reached.
func (u Usage) Level() Threshold {
	switch {
	case u.Unlimited():
		return NoThreshold
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
