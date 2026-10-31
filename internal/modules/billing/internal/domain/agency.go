package domain

import (
	"time"
)

// Agency is an agency account and the plan its stores are on (ADR-0059).
type Agency struct {
	Account
	PlanCode  string // the stores renew into this code's newest version
	Stores    int64
	CreatedAt time.Time
}

// FirstAgencyPeriod is the first member's period from now: to the first
// day of next month (UTC), then monthly with the rest of the agency
// (approved 2026-10-31).
func FirstAgencyPeriod(now time.Time) (start, end time.Time) {
	now = now.UTC()
	return now, PeriodOf(now).AddDate(0, 1, 0)
}

// JoinAgency is a store's subscription once it joins an agency on plan p
// (a version of the agency's code). peer is a current member's: the
// store shares its version, status and period, so the agency is billed
// once a month for all of them, and a store joining mid-period costs
// nothing more (the agency's price is flat). The first member starts
// now. The store keeps whether it used its trial.
func JoinAgency(own Subscription, p Plan, peer *Subscription, now time.Time) Subscription {
	s := Subscription{PlanID: p.ID, Kind: KindAgency, Status: Active, Cycle: Monthly, TrialUsed: own.TrialUsed}
	if peer == nil {
		s.PeriodStart, s.PeriodEnd = FirstAgencyPeriod(now)
		return s
	}
	s.PlanID, s.Status, s.Cycle = peer.PlanID, peer.Status, peer.Cycle
	s.PeriodStart, s.PeriodEnd, s.GraceUntil, s.SuspendedAt = peer.PeriodStart, peer.PeriodEnd, peer.GraceUntil, peer.SuspendedAt
	s.CancelAtPeriodEnd = peer.CancelAtPeriodEnd
	return s
}

// LeaveAgency is a store's subscription once it leaves an agency: the
// free plan, with no account until its next invoice opens its own
// (approved 2026-10-31).
func LeaveAgency(own Subscription, free Plan) Subscription {
	s := OnFree(free)
	s.TrialUsed = own.TrialUsed
	return s
}

// Held reports whether a store in s is held for billing: suspended for
// an unpaid invoice, or cancelled after the retention (ADR-0051).
func (s Subscription) Held() bool { return s.Status == Suspended || s.Status == Cancelled }
