package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestChoose: the approved rules for a merchant's choice (ADR-0060).
func TestChoose(t *testing.T) {
	t.Parallel()
	growth := domain.Plan{ID: ulid.ID{7}, Code: "growth", Kind: domain.KindSubscription, TrialDays: 14}
	onFreeSub := domain.OnFree(free)
	trial := domain.Subscription{PlanID: pro.ID, Kind: pro.Kind, Status: domain.Trialing, TrialEndsAt: t0.Add(5 * day), TrialUsed: true}
	onPays := domain.Subscription{PlanID: pays.ID, Kind: domain.KindPays, Status: domain.Active}
	paid := domain.Subscription{PlanID: pro.ID, Kind: pro.Kind, Status: domain.Active, Cycle: domain.Monthly,
		PeriodStart: t0, PeriodEnd: t0.AddDate(0, 1, 0), TrialUsed: true}
	cancelled := paid
	cancelled.CancelAtPeriodEnd = true
	late := paid
	late.Status, late.GraceUntil = domain.PastDue, t0.Add(7*day)

	tests := []struct {
		name    string
		s       domain.Subscription
		current domain.Plan
		p       domain.Plan
		c       domain.Cycle
		action  domain.Action
		err     error
		check   func(domain.Subscription) bool
	}{
		{"free to paid: pay now", onFreeSub, free, growth, domain.Monthly, domain.ActPay, nil, nil},
		{"trial to paid: pay now", trial, pro, pro, domain.Annual, domain.ActPay, nil, nil},
		{"pays to paid: pay now", onPays, pays, growth, domain.Monthly, domain.ActPay, nil, nil},
		{"free to pays: at once", onFreeSub, free, pays, "", domain.ActApply, nil, func(n domain.Subscription) bool {
			return n.PlanID == pays.ID && n.Kind == domain.KindPays && n.Status == domain.Active
		}},
		{"trial to free: at once, trial kept used", trial, pro, free, "", domain.ActApply, nil, func(n domain.Subscription) bool {
			return n.PlanID == free.ID && n.TrialUsed
		}},
		{"paid to another paid: at renewal", paid, pro, growth, domain.Monthly, domain.ActSchedule, nil, func(n domain.Subscription) bool {
			return n.NextPlanID == growth.ID && n.NextCycle == "" && !n.CancelAtPeriodEnd && n.PlanID == pro.ID
		}},
		{"paid to annual: at renewal", paid, pro, pro, domain.Annual, domain.ActSchedule, nil, func(n domain.Subscription) bool {
			return n.NextPlanID.IsZero() && n.NextCycle == domain.Annual
		}},
		{"paid to pays: after the period", paid, pro, pays, "", domain.ActSchedule, nil, func(n domain.Subscription) bool {
			return n.CancelAtPeriodEnd && n.NextPlanID == pays.ID
		}},
		{"paid to free: after the period", paid, pro, free, "", domain.ActSchedule, nil, func(n domain.Subscription) bool {
			return n.CancelAtPeriodEnd && n.NextPlanID.IsZero()
		}},
		{"the current plan takes a cancellation back", cancelled, pro, pro, domain.Monthly, domain.ActSchedule, nil, func(n domain.Subscription) bool {
			return !n.CancelAtPeriodEnd && n.NextPlanID.IsZero()
		}},
		{"no change", paid, pro, pro, domain.Monthly, "", domain.ErrNoChange, nil},
		{"free to free", onFreeSub, free, free, "", "", domain.ErrNoChange, nil},
		{"pays to pays", onPays, pays, pays, "", "", domain.ErrNoChange, nil},
		{"past due pays first", late, pro, growth, domain.Monthly, "", domain.ErrSettleFirst, nil},
		{"agency plans are the platform's", onFreeSub, free, agency, domain.Monthly, "", domain.ErrAgencyPlan, nil},
		{"a paid plan needs a cycle", onFreeSub, free, growth, "", "", domain.ErrNeedsCycle, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.s.Choose(tt.current, tt.p, free, tt.c, t0)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err != nil {
				return
			}
			if got.Action != tt.action || tt.check != nil && !tt.check(got.Next) {
				t.Fatalf("choice = %+v", got)
			}
		})
	}
}

func TestScheduledChangesApply(t *testing.T) {
	t.Parallel()
	growth := domain.Plan{ID: ulid.ID{7}, Code: "growth", Kind: domain.KindSubscription}
	s := active(t)

	// A renewal to the scheduled cycle starts a year.
	s.NextCycle = domain.Annual
	renewed, changed, err := s.Settle(true)
	if err != nil || !changed || renewed.Cycle != domain.Annual || !renewed.PeriodEnd.Equal(s.PeriodEnd.AddDate(1, 0, 0)) || renewed.NextCycle != "" {
		t.Fatalf("renewed = %+v, %v", renewed, err)
	}

	// A cancellation to pay as you sell lands there.
	s = active(t)
	s.CancelAtPeriodEnd, s.NextPlanID = true, pays.ID
	ended, err := s.EndPeriod(s.PeriodEnd, free, &pays)
	if err != nil || ended.PlanID != pays.ID || ended.Kind != domain.KindPays || ended.Status != domain.Active || !ended.PeriodEnd.IsZero() {
		t.Fatalf("ended = %+v, %v", ended, err)
	}

	// The first period starts when it is paid.
	paidAt := t0.Add(3 * time.Hour)
	started, err := domain.OnFree(free).StartPaid(growth, domain.Monthly, paidAt)
	if err != nil || started.Status != domain.Active || !started.PeriodStart.Equal(paidAt) || !started.PeriodEnd.Equal(paidAt.AddDate(0, 1, 0)) {
		t.Fatalf("started = %+v, %v", started, err)
	}
}
