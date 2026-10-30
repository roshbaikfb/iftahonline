package app_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestBillingHold: a held store resolves as held, so its storefront shows
// a maintenance page; the platform's suspension is separate and wins;
// each change invalidates the store's hosts, once (ADR-0051).
func TestBillingHold(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, st := f.createStore(t, f.slug("hold"))
	host := st.Slug + ".iftahonline.com"
	resolve := func() string {
		t.Helper()
		var status string
		if err := f.app.QueryRow(t.Context(), `SELECT store_status FROM app.resolve_host($1)`, host).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	hold := func(ctx context.Context, on bool) {
		t.Helper()
		if err := postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error { return f.svc.SetBillingHoldTx(ctx, tx, on) }); err != nil {
			t.Fatal(err)
		}
	}
	jobs := func() int { return len(f.jobsFor(t, "tenancy.hosts.invalidate", st.ID)) }

	before := jobs()
	hold(ctx, true)
	if got := resolve(); got != "held" {
		t.Fatalf("held store resolves %q", got)
	}
	if after := jobs(); after != before+2 {
		t.Fatalf("holding must invalidate hosts: %d -> %d jobs", before, after)
	}
	hold(ctx, true) // already held: nothing to do
	if after := jobs(); after != before+2 {
		t.Fatalf("a repeated hold invalidated again: %d jobs", after)
	}
	if _, err := f.svc.SetStatus(ctx, domain.StatusSuspended); err != nil {
		t.Fatal(err)
	}
	hold(ctx, false)
	if got := resolve(); got != "suspended" {
		t.Fatalf("releasing the hold lifted the suspension: %q", got)
	}
	if _, err := f.svc.SetStatus(ctx, domain.StatusActive); err != nil {
		t.Fatal(err)
	}
	if got := resolve(); got != "active" {
		t.Fatalf("released and reactivated: %q", got)
	}
	if st, ok := domain.ParseResolutionStatus("held"); !ok || st != domain.StatusHeld {
		t.Error("held is a resolution status")
	}
	if _, ok := domain.ParseStoreStatus("held"); ok {
		t.Error("held is not a store's own status")
	}
}
