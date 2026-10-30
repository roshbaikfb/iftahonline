package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestInvitationsWithinThePlan: team seats are members (the owner
// included) and open invitations; past the plan's it is a 403
// plan_limit, and a revoked invitation gives its seat back (ADR-0045).
func TestInvitationsWithinThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ent, err := entitlements.New(entitlements.Deps{Pool: f.app, Source: entitlements.Fixed{entitlements.Staff: {Value: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	d := f.deps
	d.Plan = ent.Entitlements()
	svc := app.NewService(d)
	owner, _ := f.signup(t)
	octx := ownerCtx(f.device(t), owner)

	inv, err := svc.Invite(octx, f.unique("a")+"@example.com", "support")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Invite(octx, f.unique("b")+"@example.com", "support")
	var e *errs.Error
	if !errors.Is(err, entitlements.ErrLimitReached) || !errors.As(err, &e) || e.Kind != errs.KindForbidden || e.Code != entitlements.PlanLimitCode {
		t.Fatalf("a third seat: %v", err)
	}
	if err = svc.RevokeInvitation(octx, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invite(octx, f.unique("c")+"@example.com", "manager"); err != nil {
		t.Fatalf("after a revocation: %v", err)
	}
}
