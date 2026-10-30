package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeImpersonator stands in for identity: it records what it was asked.
type fakeImpersonator struct {
	calls []impCall
	fail  error
}

type impCall struct {
	store  tenant.StoreID
	admin  ulid.ID
	reason string
	d      time.Duration
	write  bool
}

func (f *fakeImpersonator) open(ctx context.Context, admin ulid.ID, reason string, d time.Duration, write bool) (app.Impersonation, error) {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return app.Impersonation{}, err
	}
	f.calls = append(f.calls, impCall{store, admin, reason, d, write})
	if f.fail != nil {
		return app.Impersonation{}, f.fail
	}
	return app.Impersonation{Code: "code", CodeExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(d)}, nil
}

// TestImpersonateIsAuditedFirst: the admin log records every attempt with
// its reason before identity opens anything, so even a failed attempt is
// visible.
func TestImpersonateIsAuditedFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	store := f.createStore(t)

	imp, err := f.svc.Impersonate(ctx, store, "merchant ticket #12", 0, false)
	if err != nil || imp.Code != "code" {
		t.Fatalf("impersonate = %+v, %v", imp, err)
	}
	want := impCall{store, me.Admin.ID, "merchant ticket #12", app.DefaultImpersonation, false}
	if len(f.imp.calls) != 1 || f.imp.calls[0] != want {
		t.Fatalf("identity calls = %+v", f.imp.calls)
	}
	if got := f.svc.ImpersonationURL("abc"); got != "https://app.iftahonline.com/impersonate#code=abc" {
		t.Fatalf("url = %s", got)
	}

	f.imp.fail = errors.New("identity down")
	if _, err = f.svc.Impersonate(ctx, store, "second try, write", 60*time.Minute, true); err == nil {
		t.Fatal("identity's failure must surface")
	}
	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var metas []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && e.Action == "store.impersonated" && e.TargetID == store.String() {
			metas = append(metas, string(e.Metadata))
		}
	}
	if len(metas) != 2 {
		t.Fatalf("admin log entries = %v", metas)
	}

	// Refused before anything is recorded or opened.
	f.imp.fail = nil
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		store  tenant.StoreID
		reason string
		d      time.Duration
		kind   errs.Kind
	}{
		{"not signed in", t.Context(), store, "a reason", 0, errs.KindUnauthenticated},
		{"no reason", ctx, store, "x", 0, errs.KindValidation},
		{"too long", ctx, store, "a reason", 61 * time.Minute, errs.KindValidation},
		{"too short", ctx, store, "a reason", time.Minute, errs.KindValidation},
		{"unknown store", ctx, tenant.StoreID{ID: f.ids.New()}, "a reason", 0, errs.KindNotFound},
	} {
		if _, err := f.svc.Impersonate(tc.ctx, tc.store, tc.reason, tc.d, false); !errs.Is(err, tc.kind) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if len(f.imp.calls) != 2 {
		t.Fatalf("refused attempts reached identity: %+v", f.imp.calls)
	}
	// The CLI's service has no impersonator.
	if _, err := f.cli.Impersonate(ctx, store, "a reason", 0, false); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
