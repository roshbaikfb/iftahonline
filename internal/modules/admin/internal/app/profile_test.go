package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// TestStoreProfile: the console reads a store's owner and numbers with
// the store in ctx, for a signed-in admin and a store that exists.
func TestStoreProfile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	shop := f.createStore(t)

	var asked tenant.StoreID
	want := app.StoreProfile{Owner: &app.OwnerContact{Name: "منى", Email: "mona@example.com", Phone: "+201012345678"}, Orders30d: 7,
		LastOrderAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Products: 42}
	d := f.deps
	d.Profile = func(ctx context.Context) (app.StoreProfile, error) {
		var err error
		asked, err = tenant.FromContext(ctx)
		return want, err
	}
	svc := app.NewService(d)

	got, err := svc.StoreProfile(ctx, shop)
	if err != nil || asked != shop || got.Owner == nil || *got.Owner != *want.Owner || got.Orders30d != 7 || got.Products != 42 {
		t.Fatalf("profile = %+v (store %v), %v", got, asked, err)
	}
	if _, err = svc.StoreProfile(ctx, tenant.StoreID{ID: f.ids.New()}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	if _, err = svc.StoreProfile(t.Context(), shop); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.svc.StoreProfile(ctx, shop); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("not wired: %v", err)
	}
}
