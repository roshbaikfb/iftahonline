package app_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// plan is an Entitlements whose risk_rules flag a test sets.
type plan struct {
	entitlements.Entitlements
	rules atomic.Bool
}

func (p *plan) Allowed(_ context.Context, f entitlements.Feature) (bool, error) {
	return f != entitlements.RiskRules || p.rules.Load(), nil
}

func (p *plan) Require(ctx context.Context, f entitlements.Feature) error {
	ok, err := p.Allowed(ctx, f)
	if err != nil || ok {
		return err
	}
	return errs.Forbidden("not in the plan")
}

// TestCheck (ADR-0066): a new order's phone is scored across stores, and
// the store's rules act on its band while the plan includes them.
func TestCheck(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b, c := f.store(t), f.store(t), f.store(t)
	risky := f.uniquePhone(t)
	for i, st := range []context.Context{a, b} {
		f.learn(st, t, f.order(st, risky, 2+i, orders.StatusReturned, orders.ReasonRefused))
	}
	fresh := f.uniquePhone(t)

	// No rules: the band, no action.
	v, err := f.svc.Check(c, risky)
	if err != nil || v.Band != domain.BandHigh || v.Action != domain.NoAction {
		t.Fatalf("without rules: %+v, %v", v, err)
	}
	if _, err = f.svc.SaveRules(c, domain.Rules{HoldFrom: domain.BandHigh, VerifyFrom: domain.BandMedium}); err != nil {
		t.Fatal(err)
	}
	if v, err = f.svc.Check(c, risky); err != nil || v.Action != domain.Hold {
		t.Fatalf("hold from high: %+v, %v", v, err)
	}
	if v, err = f.svc.Check(c, fresh); err != nil || v.Band != domain.BandNew || v.Action != domain.NoAction {
		t.Fatalf("a new customer: %+v, %v", v, err)
	}
	// Another store's rules are its own.
	if v, err = f.svc.Check(a, risky); err != nil || v.Action != domain.NoAction {
		t.Fatalf("store A has no rules: %+v, %v", v, err)
	}
	// A plan without the rules: they do nothing, and cannot be turned on;
	// they can be turned off.
	f.plan.rules.Store(false)
	if v, err = f.svc.Check(c, risky); err != nil || v.Action != domain.NoAction || v.Band != domain.BandHigh {
		t.Fatalf("plan without the rules: %+v, %v", v, err)
	}
	view, err := f.svc.Rules(c)
	if err != nil || view.Available || view.Rules.HoldFrom != domain.BandHigh {
		t.Fatalf("rules view: %+v, %v", view, err)
	}
	if _, err = f.svc.SaveRules(c, domain.Rules{HoldFrom: domain.BandMedium, VerifyFrom: domain.Off}); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("turning a rule on without the plan: %v", err)
	}
	if view, err = f.svc.SaveRules(c, domain.Rules{HoldFrom: domain.Off, VerifyFrom: domain.Off}); err != nil || view.Rules.Active() {
		t.Fatalf("turning them off: %+v, %v", view, err)
	}
	if _, err = f.svc.SaveRules(c, domain.Rules{HoldFrom: domain.BandLow, VerifyFrom: domain.Off}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a rule from low: %v", err)
	}
	if _, err = f.svc.Check(t.Context(), risky); err == nil {
		t.Fatal("Check needs a tenant")
	}
}
