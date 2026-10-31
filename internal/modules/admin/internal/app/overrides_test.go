package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// TestStoreOverridesAreAuditedFirst: per-store flags change the store's
// grants at once and are in the admin log with their reason (ADR-0057).
func TestStoreOverridesAreAuditedFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	store := f.createStore(t)

	g, err := f.svc.SetOverride(ctx, store, entitlements.Override{Feature: entitlements.Coupons, Grant: entitlements.Grant{Value: 1},
		ExpiresAt: time.Now().Add(24 * time.Hour), Reason: "promo month"})
	if err != nil || g.Effective[entitlements.Coupons].Value != 1 || g.Plan[entitlements.Coupons].Value != 0 || len(g.Overrides) != 1 {
		t.Fatalf("set = %+v, %v", g, err)
	}
	if g, err = f.svc.StoreGrants(ctx, store); err != nil || g.Overrides[0].Reason != "promo month" {
		t.Fatalf("grants = %+v, %v", g, err)
	}
	if g, err = f.svc.ClearOverride(ctx, store, entitlements.Coupons, "promo over"); err != nil || len(g.Overrides) != 0 || g.Effective[entitlements.Coupons].Value != 0 {
		t.Fatalf("clear = %+v, %v", g, err)
	}
	// Refused by entitlements after the log: a second clear.
	if _, err = f.svc.ClearOverride(ctx, store, entitlements.Coupons, "again"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("clear twice: %v", err)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && e.TargetID == store.String() {
			got = append(got, e.Action)
		}
	}
	if strings.Join(got, ",") != "store.feature_override_cleared,store.feature_override_cleared,store.feature_overridden" {
		t.Fatalf("admin log = %v", got)
	}

	for name, err := range map[string]error{
		"no reason": func() error {
			_, e := f.svc.SetOverride(ctx, store, entitlements.Override{Feature: entitlements.Coupons, Grant: entitlements.Grant{Value: 1}})
			return e
		}(),
		"flag of 2": func() error {
			_, e := f.svc.SetOverride(ctx, store, entitlements.Override{Feature: entitlements.Coupons, Grant: entitlements.Grant{Value: 2}, Reason: "promo"})
			return e
		}(),
		"clear, no reason": func() error { _, e := f.svc.ClearOverride(ctx, store, entitlements.Coupons, ""); return e }(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = f.svc.StoreGrants(ctx, tenant.StoreID{ID: f.ids.New()}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	if _, err = f.svc.StoreGrants(t.Context(), store); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.cli.StoreGrants(ctx, store); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
