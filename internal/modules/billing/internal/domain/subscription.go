// Package domain holds the billing rules (ADR-0045): plans as data, what
// they grant, and the subscription lifecycle a store moves through. It
// imports only the standard library and internal/platform (CLAUDE.md
// rule 3).
//
// Nothing here, or anywhere, branches on a plan's code: a plan is what
// its kind and its grants say it is (P7 acceptance; see
// internal/tools/noplannames).
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Kind is how a plan is paid for.
type Kind string

// Kinds.
const (
	KindFree         Kind = "free"         // the fallback: no payment, ads on the store
	KindPays         Kind = "pays"         // no subscription; a fee on delivered orders
	KindSubscription Kind = "subscription" // a monthly or annual price for one store
	KindAgency       Kind = "agency"       // one price for many stores, one invoice
)

// Paid reports whether a store on k is billed for a period.
func (k Kind) Paid() bool { return k == KindSubscription || k == KindAgency }

// Plan is one version of a plan.
type Plan struct {
	ID        ulid.ID
	Code      string // shown to platform staff; never compared (see the package comment)
	Version   int32
	Kind      Kind
	NameAr    string
	NameEn    string
	TrialDays int32
	Public    bool
	Active    bool
	Sort      int32
}

// Status is where a subscription is in its lifecycle.
type Status string

// Statuses.
const (
	Trialing  Status = "trialing"  // a paid plan's free trial
	Active    Status = "active"    // the free plan, pay as you sell, or a paid period
	PastDue   Status = "past_due"  // a renewal failed; the plan still applies until GraceUntil
	Suspended Status = "suspended" // not paid after grace: the store is closed, its data kept
	Cancelled Status = "cancelled" // suspended past the retention: the data may be deleted
)

// Cycle is how often a paid plan is billed.
type Cycle string

// Cycles.
const (
	Monthly Cycle = "monthly"
	Annual  Cycle = "annual"
)

// Errors.
var (
	// ErrTransition is a lifecycle step the subscription cannot take now.
	ErrTransition = errors.New("billing: subscription cannot do that now")
)

// Policy is how long the lifecycle waits.
type Policy struct {
	Grace     time.Duration // from a failed renewal to suspension
	Retention time.Duration // from suspension to cancellation
}

// DefaultPolicy is 7 days of grace and 90 days of kept data (approved
// 2026-10-30).
func DefaultPolicy() Policy { return Policy{Grace: 7 * 24 * time.Hour, Retention: 90 * 24 * time.Hour} }

// Subscription is a store's plan and where it stands. Zero times are
// unset.
type Subscription struct {
	PlanID            ulid.ID
	Kind              Kind // the plan's
	Status            Status
	Cycle             Cycle // a paid plan's; "" otherwise
	TrialEndsAt       time.Time
	PeriodStart       time.Time
	PeriodEnd         time.Time
	GraceUntil        time.Time
	SuspendedAt       time.Time
	CancelAtPeriodEnd bool
	TrialUsed         bool // a store gets one trial
}

// OnFree is a store on the free plan; a store without a subscription
// is on it.
func OnFree(free Plan) Subscription {
	return Subscription{PlanID: free.ID, Kind: free.Kind, Status: Active}
}

func refuse(s Subscription, step string) error {
	return fmt.Errorf("%w: %s from %s", ErrTransition, step, s.Status)
}

// PlanApplies reports whether the store gets its plan's grants at now.
// When it does not, it gets the free plan's.
func (s Subscription) PlanApplies(now time.Time) bool {
	switch s.Status {
	case Trialing:
		return now.Before(s.TrialEndsAt)
	case Active, PastDue:
		return true
	default:
		return false
	}
}

// onFreePlan reports whether the store is on the free plan in effect:
// on it, or past a trial that has not been ended yet.
func (s Subscription) onFreePlan(now time.Time) bool {
	return s.Kind == KindFree || (s.Status == Trialing && !s.PlanApplies(now))
}

// StartTrial starts p's free trial: once per store, from the free plan,
// for a paid plan that has one.
func (s Subscription) StartTrial(p Plan, now time.Time) (Subscription, error) {
	if s.TrialUsed || p.TrialDays <= 0 || !p.Kind.Paid() || !s.onFreePlan(now) {
		return Subscription{}, refuse(s, "trial")
	}
	return Subscription{
		PlanID: p.ID, Kind: p.Kind, Status: Trialing, TrialUsed: true,
		TrialEndsAt: now.Add(time.Duration(p.TrialDays) * 24 * time.Hour),
	}, nil
}

// Activate starts a paid period of p: a first payment, a renewal, an
// upgrade, or the payment that restores a past-due, suspended or
// cancelled store.
func (s Subscription) Activate(p Plan, c Cycle, start, end time.Time) (Subscription, error) {
	if p.ID.IsZero() || p.Kind == KindFree || (c != Monthly && c != Annual) || !start.Before(end) {
		return Subscription{}, refuse(s, "activate")
	}
	return Subscription{
		PlanID: p.ID, Kind: p.Kind, Status: Active, Cycle: c,
		PeriodStart: start, PeriodEnd: end, TrialUsed: s.TrialUsed,
	}, nil
}

// RenewalDue reports whether a paid period ended and should be billed
// again.
func (s Subscription) RenewalDue(now time.Time) bool {
	return s.Status == Active && s.Kind.Paid() && !s.CancelAtPeriodEnd && !s.PeriodEnd.IsZero() && !now.Before(s.PeriodEnd)
}

// PaymentFailed marks a paid plan's renewal unpaid: the plan applies
// for the grace period while the payment is retried.
func (s Subscription) PaymentFailed(now time.Time, pol Policy) (Subscription, error) {
	if s.Status != Active || !s.Kind.Paid() {
		return Subscription{}, refuse(s, "payment failed")
	}
	s.Status, s.GraceUntil = PastDue, now.Add(pol.Grace)
	return s, nil
}

// Suspend closes a store whose grace ran out unpaid. Its data is kept.
func (s Subscription) Suspend(now time.Time) (Subscription, error) {
	if s.Status != PastDue || now.Before(s.GraceUntil) {
		return Subscription{}, refuse(s, "suspend")
	}
	s.Status, s.SuspendedAt = Suspended, now
	return s, nil
}

// Close cancels a subscription suspended for the whole retention: its
// data may then be deleted.
func (s Subscription) Close(now time.Time, pol Policy) (Subscription, error) {
	if s.Status != Suspended || now.Before(s.SuspendedAt.Add(pol.Retention)) {
		return Subscription{}, refuse(s, "close")
	}
	s.Status = Cancelled
	return s, nil
}

// CancelAtEnd is the merchant cancelling: the plan runs to its period's
// end, then the store moves to the free plan.
func (s Subscription) CancelAtEnd() (Subscription, error) {
	if s.Status != Active || !s.Kind.Paid() || s.CancelAtPeriodEnd {
		return Subscription{}, refuse(s, "cancel")
	}
	s.CancelAtPeriodEnd = true
	return s, nil
}

// Resume takes back a cancellation before the period ends.
func (s Subscription) Resume() (Subscription, error) {
	if s.Status != Active || !s.CancelAtPeriodEnd {
		return Subscription{}, refuse(s, "resume")
	}
	s.CancelAtPeriodEnd = false
	return s, nil
}

// EndPeriod moves the store to the free plan when its trial or its
// cancelled period has ended.
func (s Subscription) EndPeriod(now time.Time, free Plan) (Subscription, error) {
	trialOver := s.Status == Trialing && !now.Before(s.TrialEndsAt)
	cancelled := s.Status == Active && s.CancelAtPeriodEnd && !now.Before(s.PeriodEnd)
	if !trialOver && !cancelled {
		return Subscription{}, refuse(s, "end period")
	}
	f := OnFree(free)
	f.TrialUsed = s.TrialUsed
	return f, nil
}

// ParseStatus validates a stored status.
func ParseStatus(v string) (Status, error) {
	switch s := Status(v); s {
	case Trialing, Active, PastDue, Suspended, Cancelled:
		return s, nil
	}
	return "", invalid("status", v)
}

// ParseCycle validates a billing cycle from input.
func ParseCycle(v string) (Cycle, error) {
	switch c := Cycle(v); c {
	case Monthly, Annual:
		return c, nil
	}
	return "", invalid("cycle", v)
}

// ParseKind validates a stored plan kind.
func ParseKind(v string) (Kind, error) {
	switch k := Kind(v); k {
	case KindFree, KindPays, KindSubscription, KindAgency:
		return k, nil
	}
	return "", invalid("kind", v)
}

func invalid(field, v string) error {
	return errs.Validation(errs.FieldError{Field: field, Code: "invalid", Message: fmt.Sprintf("unknown %s %q", field, v)})
}
