package app_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// TestAgenciesFromTheConsole: an agency is opened, stores join and leave
// it, and every change is in the admin log (ADR-0059).
func TestAgenciesFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	shop := f.createStore(t)

	if _, err := f.svc.CreateAgency(ctx, "وكالة", "EGP", "agency", "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := f.svc.CreateAgency(ctx, "وكالة", "EGP", "pro", "new agency"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("not an agency plan: %v", err)
	}
	d, err := f.svc.CreateAgency(ctx, "وكالة الدلتا", "EGP", "agency", "new agency")
	if err != nil || d.Agency.Name != "وكالة الدلتا" || len(d.Stores) != 0 {
		t.Fatalf("create = %+v, %v", d, err)
	}
	agency := d.Agency.ID

	if d, err = f.svc.AddStore(ctx, agency, shop, "moving to the agency"); err != nil || len(d.Stores) != 1 || d.Stores[0] != shop.ID {
		t.Fatalf("add = %+v, %v", d, err)
	}
	if _, err = f.svc.AddStore(ctx, agency, shop, "again"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("add twice: %v", err)
	}
	if _, err = f.svc.AddStore(ctx, agency, tenant.StoreID{ID: f.ids.New()}, "unknown store"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	as, err := f.svc.Agencies(ctx)
	if err != nil || len(as) == 0 {
		t.Fatalf("agencies = %v, %v", as, err)
	}
	if d, err = f.svc.RemoveStore(ctx, agency, shop, "left the agency"); err != nil || len(d.Stores) != 0 {
		t.Fatalf("remove = %+v, %v", d, err)
	}
	if _, err = f.svc.RemoveStore(ctx, agency, shop, "again"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("remove a non-member: %v", err)
	}
	if _, err = f.svc.Agency(ctx, f.ids.New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown agency: %v", err)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && (e.TargetID == shop.String() || e.TargetID == agency.String()) {
			got = append(got, e.Action)
		}
	}
	if strings.Join(got, ",") != "store.agency_left,store.agency_joined,store.agency_joined,agency.created" {
		t.Fatalf("admin log = %v", got)
	}
	if _, err = f.svc.Agencies(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.cli.Agencies(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
