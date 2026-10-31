package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	t0     = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	ids    = ulid.NewGenerator()
	free   = domain.Plan{ID: ids.New(), Kind: domain.KindFree}
	pro    = domain.Plan{ID: ids.New(), Kind: domain.KindSubscription, TrialDays: 14}
	pays   = domain.Plan{ID: ids.New(), Kind: domain.KindPays}
	agency = domain.Plan{ID: ids.New(), Kind: domain.KindAgency, TrialDays: 14}
	policy = domain.DefaultPolicy()
)

const day = 24 * time.Hour

func onFree() domain.Subscription { return domain.OnFree(free) }

func active(t *testing.T) domain.Subscription {
	t.Helper()
	s, err := onFree().Activate(pro, domain.Monthly, t0, t0.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pastDue(t *testing.T) domain.Subscription {
	t.Helper()
	s, err := active(t).PaymentFailed(t0.AddDate(0, 1, 0), policy)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func suspended(t *testing.T) domain.Subscription {
	t.Helper()
	s, err := pastDue(t).Suspend(t0.AddDate(0, 1, 0).Add(policy.Grace))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlanApplies(t *testing.T) {
	t.Parallel()
	trial, err := onFree().StartTrial(pro, t0)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		s    domain.Subscription
		at   time.Time
		want bool
	}{
		{"free", onFree(), t0, true},
		{"trial running", trial, t0.Add(14*day - time.Second), true},
		{"trial over", trial, t0.Add(14 * day), false},
		{"active", active(t), t0.AddDate(0, 2, 0), true}, // a late renewal job does not take the plan away
		{"past due, in grace", pastDue(t), t0.AddDate(0, 1, 3), true},
		{"suspended", suspended(t), t0.AddDate(0, 2, 0), false},
	}
	for _, tt := range tests {
		if got := tt.s.PlanApplies(tt.at); got != tt.want {
			t.Errorf("%s: PlanApplies = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestStartTrial(t *testing.T) {
	t.Parallel()
	s, err := onFree().StartTrial(pro, t0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != domain.Trialing || s.PlanID != pro.ID || !s.TrialEndsAt.Equal(t0.Add(14*day)) || !s.TrialUsed {
		t.Fatalf("trial = %+v", s)
	}
	if _, err = onFree().StartTrial(agency, t0); err != nil {
		t.Errorf("agency trial: %v", err)
	}
	ended, err := s.EndPeriod(t0.Add(14*day), free, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		s    domain.Subscription
		plan domain.Plan
	}{
		{"a second trial", ended, pro},
		{"while trialing", s, pro},
		{"while paying", active(t), pro},
		{"of the free plan", onFree(), free},
		{"of pay as you sell", onFree(), pays},
		{"of a plan without trial", onFree(), domain.Plan{ID: ids.New(), Kind: domain.KindSubscription}},
	}
	for _, tt := range tests {
		if _, err := tt.s.StartTrial(tt.plan, t0); !errors.Is(err, domain.ErrTransition) {
			t.Errorf("%s: err = %v, want ErrTransition", tt.name, err)
		}
	}
}

func TestEndOfTrialFallsBackToFree(t *testing.T) {
	t.Parallel()
	s, err := onFree().StartTrial(pro, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EndPeriod(t0.Add(13*day), free, nil); !errors.Is(err, domain.ErrTransition) {
		t.Fatalf("before the end: err = %v", err)
	}
	s, err = s.EndPeriod(t0.Add(14*day), free, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != domain.Active || s.PlanID != free.ID || !s.TrialUsed || !s.TrialEndsAt.IsZero() {
		t.Fatalf("after the trial = %+v", s)
	}
}

func TestActivate(t *testing.T) {
	t.Parallel()
	end := t0.AddDate(1, 0, 0)
	trial, err := onFree().StartTrial(pro, t0)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := suspended(t).Close(t0.AddDate(0, 1, 0).Add(policy.Grace+policy.Retention), policy)
	if err != nil {
		t.Fatal(err)
	}
	for name, from := range map[string]domain.Subscription{
		"free": onFree(), "trial": trial, "active": active(t), "past due": pastDue(t), "suspended": suspended(t), "cancelled": cancelled,
	} {
		s, err := from.Activate(pro, domain.Annual, t0, end)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if s.Status != domain.Active || s.Cycle != domain.Annual || !s.PeriodEnd.Equal(end) ||
			!s.GraceUntil.IsZero() || !s.SuspendedAt.IsZero() || s.CancelAtPeriodEnd || s.TrialUsed != from.TrialUsed {
			t.Errorf("%s: activated = %+v", name, s)
		}
	}
	bad := []struct {
		name       string
		plan       domain.Plan
		cycle      domain.Cycle
		start, end time.Time
	}{
		{"the free plan", free, domain.Monthly, t0, end},
		{"no cycle", pro, "", t0, end},
		{"an empty period", pro, domain.Monthly, t0, t0},
		{"no plan", domain.Plan{}, domain.Monthly, t0, end},
	}
	for _, tt := range bad {
		if _, err := onFree().Activate(tt.plan, tt.cycle, tt.start, tt.end); !errors.Is(err, domain.ErrTransition) {
			t.Errorf("%s: err = %v, want ErrTransition", tt.name, err)
		}
	}
}

// TestDunning walks a failed renewal to suspension, closure, and a
// payment that brings the store back at each stage (P7 acceptance; the
// jobs that drive it are tested in 7d).
func TestDunning(t *testing.T) {
	t.Parallel()
	failedAt := t0.AddDate(0, 1, 0)
	s := pastDue(t)
	if s.Status != domain.PastDue || !s.GraceUntil.Equal(failedAt.Add(policy.Grace)) {
		t.Fatalf("past due = %+v", s)
	}
	if _, err := s.PaymentFailed(failedAt.Add(day), policy); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("a retry failing again moves nothing: err = %v", err)
	}
	if _, err := s.Suspend(failedAt.Add(policy.Grace - time.Second)); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("suspend within grace: err = %v", err)
	}
	s, err := s.Suspend(failedAt.Add(policy.Grace))
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != domain.Suspended || !s.SuspendedAt.Equal(failedAt.Add(policy.Grace)) {
		t.Fatalf("suspended = %+v", s)
	}
	if _, err = s.Close(s.SuspendedAt.Add(policy.Retention-time.Second), policy); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("close before the retention: err = %v", err)
	}
	closed, err := s.Close(s.SuspendedAt.Add(policy.Retention), policy)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != domain.Cancelled || !closed.SuspendedAt.Equal(s.SuspendedAt) {
		t.Fatalf("closed = %+v", closed)
	}
	restored, err := s.Activate(pro, domain.Monthly, failedAt.AddDate(0, 0, 10), failedAt.AddDate(0, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != domain.Active || !restored.PlanApplies(failedAt.AddDate(0, 0, 10)) {
		t.Fatalf("restored = %+v", restored)
	}
	for name, from := range map[string]domain.Subscription{"free": onFree(), "suspended": s, "cancelled": closed} {
		if _, err := from.PaymentFailed(failedAt, policy); !errors.Is(err, domain.ErrTransition) {
			t.Errorf("PaymentFailed from %s: err = %v", name, err)
		}
	}
	if _, err := active(t).Suspend(failedAt.AddDate(1, 0, 0)); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("suspend an active store: err = %v", err)
	}
	if _, err := closed.Close(closed.SuspendedAt.AddDate(1, 0, 0), policy); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("close twice: err = %v", err)
	}
}

func TestCancelAtPeriodEnd(t *testing.T) {
	t.Parallel()
	s, err := active(t).CancelAtEnd()
	if err != nil {
		t.Fatal(err)
	}
	if !s.CancelAtPeriodEnd || !s.PlanApplies(s.PeriodEnd.Add(-time.Second)) {
		t.Fatalf("cancelling = %+v", s)
	}
	if r, rErr := s.Resume(); rErr != nil || r.CancelAtPeriodEnd {
		t.Errorf("resume = %+v, %v", r, rErr)
	}
	if _, err = s.EndPeriod(s.PeriodEnd.Add(-time.Second), free, nil); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("end before the period's end: err = %v", err)
	}
	ended, err := s.EndPeriod(s.PeriodEnd, free, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Status != domain.Active || ended.PlanID != free.ID || ended.Cycle != "" || !ended.PeriodEnd.IsZero() || ended.CancelAtPeriodEnd {
		t.Fatalf("on free = %+v", ended)
	}
	if _, err = active(t).EndPeriod(t0.AddDate(0, 2, 0), free, nil); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("a period ends without a cancellation (renewal is a payment): err = %v", err)
	}
	for name, from := range map[string]domain.Subscription{"free": onFree(), "past due": pastDue(t), "suspended": suspended(t)} {
		if _, cErr := from.CancelAtEnd(); !errors.Is(cErr, domain.ErrTransition) {
			t.Errorf("CancelAtEnd from %s: err = %v", name, cErr)
		}
	}
	if _, err = active(t).Resume(); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("resume without a cancellation: err = %v", err)
	}
}

func TestRenewalDue(t *testing.T) {
	t.Parallel()
	s := active(t)
	if s.RenewalDue(s.PeriodEnd.Add(-time.Second)) || !s.RenewalDue(s.PeriodEnd) {
		t.Error("renewal is due at the period's end")
	}
	c, err := s.CancelAtEnd()
	if err != nil {
		t.Fatal(err)
	}
	if c.RenewalDue(s.PeriodEnd) || onFree().RenewalDue(t0.AddDate(5, 0, 0)) || pastDue(t).RenewalDue(t0.AddDate(1, 0, 0)) {
		t.Error("only an active paid subscription that is not cancelling renews")
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"trialing", "active", "past_due", "suspended", "cancelled"} {
		if st, err := domain.ParseStatus(s); err != nil || string(st) != s {
			t.Errorf("ParseStatus(%q) = %q, %v", s, st, err)
		}
	}
	for _, s := range []string{"monthly", "annual"} {
		if c, err := domain.ParseCycle(s); err != nil || string(c) != s {
			t.Errorf("ParseCycle(%q) = %q, %v", s, c, err)
		}
	}
	for _, s := range []string{"free", "pays", "subscription", "agency"} {
		if k, err := domain.ParseKind(s); err != nil || string(k) != s {
			t.Errorf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}
	for _, s := range []string{"", "Active", "paused"} {
		if _, err := domain.ParseStatus(s); err == nil {
			t.Errorf("ParseStatus(%q) accepted", s)
		}
		if _, err := domain.ParseCycle(s); err == nil {
			t.Errorf("ParseCycle(%q) accepted", s)
		}
		if _, err := domain.ParseKind(s); err == nil {
			t.Errorf("ParseKind(%q) accepted", s)
		}
	}
}
