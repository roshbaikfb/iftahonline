package domain

import "slices"

// The console's alerts (ADR-0080): what a check finds, when an alert
// opens and resolves, and who hears of it.

// AlertKind is one kind of problem the console watches.
type AlertKind string

// The kinds, the red first.
const (
	AlertDuplicates AlertKind = "duplicates" // payments of paid invoices, to refund
	AlertDiscarded  AlertKind = "discarded"  // jobs given up in the last 24 hours
	AlertSlowQueue  AlertKind = "slow"       // the oldest waiting job is too old
	AlertRedis      AlertKind = "redis"      // Redis does not answer
	AlertOverdue    AlertKind = "overdue"    // invoices past due
	AlertHeld       AlertKind = "held"       // stores held for an unpaid invoice
	AlertPastDue    AlertKind = "past_due"   // subscriptions in their grace period
)

// AlertKinds are every kind, in the order the console lists them.
func AlertKinds() []AlertKind {
	return []AlertKind{AlertDuplicates, AlertDiscarded, AlertSlowQueue, AlertRedis, AlertOverdue, AlertHeld, AlertPastDue}
}

// Severity is red (act now) or amber (look today).
type Severity string

// The severities.
const (
	SeverityRed   Severity = "red"
	SeverityAmber Severity = "amber"
)

// Severity of the kind.
func (k AlertKind) Severity() Severity {
	switch k {
	case AlertDuplicates, AlertDiscarded, AlertSlowQueue, AlertRedis:
		return SeverityRed
	default:
		return SeverityAmber
	}
}

// For reports whether an admin in role hears of this kind: owners of
// everything, finance of money, operations of the jobs.
func (k AlertKind) For(r Role) bool {
	switch r {
	case RoleOwner:
		return true
	case RoleFinance:
		return slices.Contains([]AlertKind{AlertDuplicates, AlertOverdue, AlertHeld, AlertPastDue}, k)
	case RoleOperations:
		return k == AlertDiscarded || k == AlertSlowQueue
	}
	return false
}

// SlowQueueSeconds is how old the oldest waiting job may be.
const SlowQueueSeconds = 600

// Signals are what one check read; a section it could not read is not
// known, and its kinds neither open nor resolve.
type Signals struct {
	RedisKnown, RedisUp                bool
	JobsKnown                          bool
	Discarded24h, OldestWaitingSeconds int64
	BillingKnown                       bool
	Overdue, PastDue                   int64
	DuplicatesKnown                    bool
	Duplicates                         int64
	StoresKnown                        bool
	Held                               int64
}

// Reading is one kind in one check: known or not, firing or not, and its
// number (the count, or the seconds the queue waits).
type Reading struct {
	Known, Firing bool
	Value         int64
}

// Read turns signals into one reading per kind.
func (s Signals) Read() map[AlertKind]Reading {
	n := func(known bool, v int64) Reading { return Reading{Known: known, Firing: known && v > 0, Value: v} }
	redis := Reading{Known: s.RedisKnown, Firing: s.RedisKnown && !s.RedisUp}
	return map[AlertKind]Reading{
		AlertDuplicates: n(s.DuplicatesKnown, s.Duplicates),
		AlertDiscarded:  n(s.JobsKnown, s.Discarded24h),
		AlertSlowQueue: {Known: s.JobsKnown, Firing: s.JobsKnown && s.OldestWaitingSeconds > SlowQueueSeconds,
			Value: s.OldestWaitingSeconds},
		AlertRedis:   redis,
		AlertOverdue: n(s.BillingKnown, s.Overdue),
		AlertHeld:    n(s.StoresKnown, s.Held),
		AlertPastDue: n(s.BillingKnown, s.PastDue),
	}
}

// AlertStreak is how many checks in a row found a kind firing (Bad) or
// clean (Good).
type AlertStreak struct{ Bad, Good int }

// StreakToChange is how many checks in a row open or resolve an alert:
// one bad or good check alone changes nothing (no flapping).
const StreakToChange = 2

// AlertStep is what a check does to one kind.
type AlertStep int

// The steps.
const (
	AlertNone    AlertStep = iota // nothing changes (or only its number, while open)
	AlertOpen                     // a new alert opens
	AlertResolve                  // the open one resolves
)

// Step advances a kind's streak by one reading, open telling whether an
// alert of it is open, and says what happens.
func Step(s AlertStreak, r Reading, open bool) (AlertStreak, AlertStep) {
	if !r.Known {
		return s, AlertNone
	}
	if r.Firing {
		s = AlertStreak{Bad: s.Bad + 1}
		if !open && s.Bad >= StreakToChange {
			return s, AlertOpen
		}
		return s, AlertNone
	}
	s = AlertStreak{Good: s.Good + 1}
	if open && s.Good >= StreakToChange {
		return s, AlertResolve
	}
	return s, AlertNone
}

// NotifyResolved reports whether an alert's resolution is sent: red
// ones only, amber ones just leave the console's list.
func (k AlertKind) NotifyResolved() bool { return k.Severity() == SeverityRed }
