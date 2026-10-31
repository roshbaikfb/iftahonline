package domain

import (
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// What a merchant's choice of plan does (ADR-0060, approved 2026-10-31).

// Action is how a choice takes effect.
type Action string

// Actions.
const (
	// ActApply changes the subscription now: the free plan or pay as you
	// sell, from the free plan, a trial or pay as you sell.
	ActApply Action = "apply"
	// ActPay needs the first period paid on the gateway's page, which then
	// starts it: a paid plan from the free plan, a trial or pay as you sell.
	ActPay Action = "pay"
	// ActSchedule takes effect when the paid period ends: another paid plan
	// or cycle at the renewal (no proration), or pay as you sell or the
	// free plan after it.
	ActSchedule Action = "schedule"
)

// Choice is what a choice does: Next is the subscription to save for
// ActApply and ActSchedule; ActPay saves nothing until the payment.
type Choice struct {
	Action Action
	Next   Subscription
}

// Choice refusals.
var (
	ErrSettleFirst = errs.Conflict("settle_first", "pay the open invoice first")
	ErrNoChange    = errs.Conflict("no_change", "the store is already on this plan")
	ErrAgencyPlan  = errs.Conflict("agency_managed", "agency plans are set up by the platform")
	ErrNeedsCycle  = errs.Validation(errs.FieldError{Field: "cycle", Code: "required", Message: "monthly or annual"})
)

// Choose is what choosing plan p (cycle c for a paid plan) does to s at
// now. current is the plan s is on; free the free plan.
func (s Subscription) Choose(current, p, free Plan, c Cycle, now time.Time) (Choice, error) {
	switch {
	case s.Status == PastDue || s.Status == Suspended || s.Status == Cancelled:
		return Choice{}, ErrSettleFirst
	case p.Kind == KindAgency || s.Kind == KindAgency:
		return Choice{}, ErrAgencyPlan
	case p.Kind.Paid() && c != Monthly && c != Annual:
		return Choice{}, ErrNeedsCycle
	}
	paidPeriod := s.Status == Active && s.Kind.Paid()
	if paidPeriod {
		return s.schedule(current, p, c)
	}
	onFree := s.onFreePlan(now)
	switch p.Kind {
	case KindFree:
		if onFree {
			return Choice{}, ErrNoChange
		}
		return Choice{Action: ActApply, Next: LeaveAgency(s, free)}, nil
	case KindPays:
		if s.Kind == KindPays && s.Status == Active {
			return Choice{}, ErrNoChange
		}
		return Choice{Action: ActApply, Next: Subscription{PlanID: p.ID, Kind: KindPays, Status: Active, TrialUsed: s.TrialUsed}}, nil
	}
	return Choice{Action: ActPay}, nil
}

// schedule is a choice made during a paid period: it waits for the
// period's end. Choosing the current plan and cycle again takes back a
// scheduled change or a cancellation.
func (s Subscription) schedule(current, p Plan, c Cycle) (Choice, error) {
	next := s
	next.CancelAtPeriodEnd, next.NextPlanID, next.NextCycle = false, ulid.ID{}, ""
	switch {
	case p.Kind == KindFree:
		next.CancelAtPeriodEnd = true
	case p.Kind == KindPays:
		next.CancelAtPeriodEnd, next.NextPlanID = true, p.ID
	case p.Code == current.Code && c == s.Cycle:
		if !s.CancelAtPeriodEnd && s.NextPlanID.IsZero() && s.NextCycle == "" {
			return Choice{}, ErrNoChange
		}
	default:
		if p.Code != current.Code {
			next.NextPlanID = p.ID
		}
		if c != s.Cycle {
			next.NextCycle = c
		}
	}
	return Choice{Action: ActSchedule, Next: next}, nil
}

// StartPaid is the subscription once the first period of p, cycle c, is
// paid at paidAt: it starts then (approved 2026-10-31).
func (s Subscription) StartPaid(p Plan, c Cycle, paidAt time.Time) (Subscription, error) {
	return s.Activate(p, c, paidAt, NextPeriodEnd(paidAt, c))
}
