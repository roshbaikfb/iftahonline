package app_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestOverrides: a store's override replaces its plan's grant at once
// (the cache is dropped), until it expires or is cleared, and only for
// that store; each change publishes GrantsChanged (ADR-0057).
func TestOverrides(t *testing.T) {
	t.Parallel()
	plan := app.Fixed{domain.Coupons: {Value: 0}, domain.Products: {Value: 50}}
	f := newFixture(t, plan)
	events := testkit.NewEvents()
	f.svc = app.NewService(app.Deps{DB: f.pool, Source: plan, IDs: f.ids, Events: events, Now: f.clock.now})
	a, b := f.store(t), f.store(t)

	if ok, err := f.svc.Allowed(a, domain.Coupons); err != nil || ok {
		t.Fatalf("before = %v, %v", ok, err) // and now cached
	}
	if err := f.svc.SetOverride(a, domain.Override{Feature: domain.Coupons, Grant: domain.Grant{Value: 1}, Reason: "promo month"}); err != nil {
		t.Fatal(err)
	}
	until := f.clock.now().Add(time.Hour)
	if err := f.svc.SetOverride(a, domain.Override{Feature: domain.Products, Grant: domain.Grant{Unlimited: true}, ExpiresAt: until, Reason: "import"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.svc.Allowed(a, domain.Coupons); err != nil || !ok {
		t.Fatalf("after = %v, %v", ok, err)
	}
	if ok, _ := f.svc.Allowed(b, domain.Coupons); ok {
		t.Fatal("B got A's override")
	}
	sg, err := f.svc.StoreGrants(a)
	if err != nil || len(sg.Overrides) != 2 || sg.Plan[domain.Products].Value != 50 || !sg.Effective[domain.Products].Unlimited ||
		sg.Overrides[0].Reason != "promo month" || sg.Overrides[1].ExpiresAt.IsZero() {
		t.Fatalf("store grants = %+v, %v", sg, err)
	}

	// Expired, the plan's grant applies again.
	f.clock.advance(2 * time.Hour)
	if sg, err = f.svc.StoreGrants(a); err != nil || sg.Effective[domain.Products] != (domain.Grant{Value: 50}) || len(sg.Overrides) != 2 {
		t.Fatalf("after expiry = %+v, %v", sg, err)
	}

	if err = f.svc.ClearOverride(a, domain.Coupons); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.svc.Allowed(a, domain.Coupons); ok {
		t.Fatal("cleared, still allowed")
	}
	if err = f.svc.ClearOverride(a, domain.Coupons); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("clear twice: %v", err)
	}
	if err = f.svc.SetOverride(a, domain.Override{Feature: "live_chat", Grant: domain.Grant{Value: 1}, Reason: "promo"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("display-only key: %v", err)
	}
	n := 0
	for _, ev := range events.All() {
		if ev.EventMeta().Type == domain.EventGrantsChanged {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d GrantsChanged, want 3 (two set, one clear)", n)
	}
}
