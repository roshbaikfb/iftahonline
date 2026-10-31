package domain_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestJoinAndLeaveAgency(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	plan := domain.Plan{ID: ulid.ID{1}, Kind: domain.KindAgency}
	own := domain.Subscription{PlanID: ulid.ID{9}, Kind: domain.KindSubscription, Status: domain.Trialing, TrialUsed: true,
		TrialEndsAt: now.Add(time.Hour)}

	first := domain.JoinAgency(own, plan, nil, now)
	if first.PlanID != plan.ID || first.Status != domain.Active || first.Cycle != domain.Monthly || !first.TrialUsed ||
		!first.PeriodStart.Equal(now) || !first.PeriodEnd.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) || !first.TrialEndsAt.IsZero() {
		t.Fatalf("first member = %+v", first)
	}

	peer := domain.Subscription{PlanID: ulid.ID{2}, Kind: domain.KindAgency, Status: domain.PastDue, Cycle: domain.Monthly,
		PeriodStart: now.AddDate(0, 0, -16), PeriodEnd: now.AddDate(0, 0, 14), GraceUntil: now.Add(48 * time.Hour)}
	joined := domain.JoinAgency(domain.Subscription{}, plan, &peer, now)
	if joined.PlanID != peer.PlanID || joined.Status != domain.PastDue || !joined.PeriodEnd.Equal(peer.PeriodEnd) ||
		!joined.GraceUntil.Equal(peer.GraceUntil) || joined.Kind != domain.KindAgency || joined.Held() {
		t.Fatalf("joined = %+v", joined)
	}
	peer.Status, peer.SuspendedAt = domain.Suspended, now
	if held := domain.JoinAgency(domain.Subscription{}, plan, &peer, now); !held.Held() {
		t.Fatalf("a suspended agency's new member is held: %+v", held)
	}

	free := domain.Plan{ID: ulid.ID{3}, Kind: domain.KindFree}
	left := domain.LeaveAgency(joined, free)
	if left.PlanID != free.ID || left.Status != domain.Active || left.Cycle != "" || !left.PeriodEnd.IsZero() || left.Held() {
		t.Fatalf("left = %+v", left)
	}
	if start, end := domain.FirstAgencyPeriod(time.Date(2026, 12, 31, 23, 0, 0, 0, time.FixedZone("x", 3*3600))); !start.Before(end) ||
		!end.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("year end = %v %v", start, end)
	}
}
