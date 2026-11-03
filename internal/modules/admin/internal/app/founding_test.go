package app_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestFoundingFromTheConsole: the program's settings need a reason, are
// checked before they are recorded, and every change is in the admin log.
func TestFoundingFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)

	p, members, err := f.svc.Founding(ctx)
	if err != nil || p.Enabled || p.Seats != 100 || p.DiscountBps != 2000 || len(members) != 0 {
		t.Fatalf("founding = %+v %v, %v", p, members, err)
	}
	on := billing.FoundingProgram{Enabled: true, Seats: 50, DiscountBps: 2500}
	if err = f.svc.SaveFounding(ctx, on, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if err = f.svc.SaveFounding(ctx, billing.FoundingProgram{Seats: 0, DiscountBps: 2000}, "no seats"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no seats: %v", err)
	}
	if err = f.svc.SaveFounding(ctx, on, "launch"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ = f.svc.Founding(ctx); p != on {
		t.Fatalf("saved = %+v", p)
	}
	if err = f.svc.SaveFounding(ctx, billing.FoundingProgram{Seats: 100, DiscountBps: 2000}, "back to the default"); err != nil {
		t.Fatal(err)
	}

	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && strings.HasPrefix(e.Action, "billing.founding") {
			got = append(got, e.Action)
		}
	}
	if strings.Join(got, ",") != "billing.founding_saved,billing.founding_saved" {
		t.Fatalf("admin log = %v", got)
	}
	if _, _, err = f.cli.Founding(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
	if _, _, err = f.svc.Founding(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}
