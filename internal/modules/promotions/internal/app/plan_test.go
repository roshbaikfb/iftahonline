package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestCouponsNeedThePlan: without coupons in the plan the merchant makes
// none, and a code made before (a lapsed trial) takes nothing off at
// checkout: for the shopper it does not exist (ADR-0046).
func TestCouponsNeedThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.Create(ctx, domain.Coupon{Code: "SUMMER10", Kind: domain.Percent, Percent: 10, Active: true}); err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{}})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Plan: ent.Entitlements()})

	_, err = svc.Create(ctx, domain.Coupon{Code: "WINTER", Kind: domain.FreeShipping, Active: true})
	if !errors.Is(err, entitlements.ErrLimitReached) || errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("create without the plan: %v", err)
	}
	var r *domain.Rejected
	if _, err = svc.Preview(ctx, "summer10", egp(t, 10000)); !errors.As(err, &r) || r.Reason != domain.NotFound {
		t.Fatalf("an old code without the plan: %v", err)
	}
	if _, err = f.svc.Preview(ctx, "summer10", egp(t, 10000)); err != nil {
		t.Fatalf("with the plan: %v", err)
	}
}
