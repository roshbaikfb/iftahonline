package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
)

func TestNextAttempt(t *testing.T) {
	t.Parallel()
	failed := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	for failures, want := range map[int]time.Duration{1: 24 * time.Hour, 2: 72 * time.Hour, 3: 120 * time.Hour} {
		got, ok := domain.NextAttempt(failed, failures)
		if !ok || !got.Equal(failed.Add(want)) {
			t.Errorf("after %d failures: %v %v, want +%v", failures, got, ok, want)
		}
	}
	for _, failures := range []int{0, 4, 9} {
		if _, ok := domain.NextAttempt(failed, failures); ok {
			t.Errorf("after %d failures: a retry", failures)
		}
	}
	if last, _ := domain.NextAttempt(failed, 3); !last.Before(failed.Add(domain.DefaultPolicy().Grace)) {
		t.Error("the last retry is inside the grace")
	}
}

func TestSettle(t *testing.T) {
	t.Parallel()
	monthly, err := onFree().Activate(pro, domain.Monthly, t0, t0.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	renewed, changed, err := monthly.Settle(true)
	if err != nil || !changed || !renewed.PeriodStart.Equal(t0.AddDate(0, 1, 0)) || !renewed.PeriodEnd.Equal(t0.AddDate(0, 2, 0)) {
		t.Fatalf("monthly renewal = %+v, %v, %v", renewed, changed, err)
	}
	annual, err := onFree().Activate(pro, domain.Annual, t0, t0.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, aErr := annual.Settle(true); aErr != nil || !r.PeriodEnd.Equal(t0.AddDate(2, 0, 0)) {
		t.Fatalf("annual renewal = %+v, %v", r, aErr)
	}
	// Restoring a past-due or suspended store, renewed or not.
	for name, from := range map[string]domain.Subscription{"past due": pastDue(t), "suspended": suspended(t)} {
		for _, renew := range []bool{true, false} {
			got, changed, sErr := from.Settle(renew)
			if sErr != nil || !changed || got.Status != domain.Active || !got.GraceUntil.IsZero() || !got.SuspendedAt.IsZero() {
				t.Errorf("%s (renew %v) = %+v, %v, %v", name, renew, got, changed, sErr)
			}
		}
	}
	// Paid fees of an active store change nothing.
	if _, changed, sErr := monthly.Settle(false); sErr != nil || changed {
		t.Errorf("fees of an active store: changed %v, %v", changed, sErr)
	}
	// Pay as you sell has no period to renew.
	paysActive := domain.Subscription{PlanID: pays.ID, Kind: domain.KindPays, Status: domain.Active}
	if _, _, sErr := paysActive.Settle(true); !errors.Is(sErr, domain.ErrTransition) {
		t.Errorf("renew pay as you sell: %v", sErr)
	}
	// Pay as you sell can fall past due, and be restored.
	pd, err := paysActive.PaymentFailed(t0, policy)
	if err != nil || pd.Status != domain.PastDue {
		t.Fatalf("pay as you sell past due = %+v, %v", pd, err)
	}
	if back, changed, sErr := pd.Settle(false); sErr != nil || !changed || back.Status != domain.Active {
		t.Fatalf("pay as you sell restored = %+v, %v", back, sErr)
	}
}
