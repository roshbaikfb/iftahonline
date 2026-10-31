package app_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func (f fixture) code(prefix string) string {
	return prefix + strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0')
		}
		return r
	}, strings.ToLower(f.ids.New().String()[16:]))
}

// TestPlanCatalogIsAuditedFirst: every change to the catalog is in the
// admin log with its reason, refused ones included (ADR-0056).
func TestPlanCatalogIsAuditedFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)

	key := f.code("t_")
	feat, err := f.svc.SaveFeature(ctx, billing.Feature{Key: key, Kind: "flag", NameAr: "ميزة", NameEn: "Perk"}, "sales asked for it")
	if err != nil || feat.Enforced {
		t.Fatalf("save feature = %+v, %v", feat, err)
	}
	fs, err := f.svc.Features(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enforced := map[string]bool{}
	for _, x := range fs {
		enforced[x.Key] = x.Enforced
	}
	if !enforced["coupons"] || enforced[key] {
		t.Fatalf("enforced = %v", enforced)
	}

	code := f.code("t_")
	v := billing.PlanVersion{
		Plan: billing.Plan{Code: code, Kind: "subscription", NameAr: "نمو", NameEn: "Growth", Public: true},
		Prices: []billing.Price{
			{Currency: "EGP", Cycle: "monthly", AmountMinor: 100}, {Currency: "SAR", Cycle: "monthly", AmountMinor: 100},
			{Currency: "AED", Cycle: "monthly", AmountMinor: 100},
		},
		Grants: billing.Grants{key: {Value: 1}},
	}
	pub, err := f.svc.PublishPlan(ctx, v, "launch growth")
	if err != nil || pub.Version != 1 {
		t.Fatalf("publish = %+v, %v", pub, err)
	}
	v.Kind = "agency" // a code keeps its kind: refused by billing, after the log
	if _, err = f.svc.PublishPlan(ctx, v, "try agency"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("kind change: %v", err)
	}
	if err = f.svc.RetirePlan(ctx, code, "growth discontinued"); err != nil {
		t.Fatal(err)
	}
	ps, err := f.svc.Plans(ctx)
	if err != nil || !slices.ContainsFunc(ps, func(p billing.PlanVersion) bool { return p.Code == code && !p.Active }) {
		t.Fatalf("plans = %v, %v", len(ps), err)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && (e.TargetID == key || e.TargetID == code) {
			got = append(got, e.Action)
			if !strings.Contains(string(e.Metadata), "reason") {
				t.Fatalf("%s without a reason: %s", e.Action, e.Metadata)
			}
		}
	}
	if strings.Join(got, ",") != "plan.retired,plan.published,plan.published,feature.saved" {
		t.Fatalf("admin log = %v", got)
	}

	// Refused before anything is recorded.
	for name, err := range map[string]error{
		"no reason":         func() error { _, e := f.svc.PublishPlan(ctx, v, "x"); return e }(),
		"invalid":           func() error { _, e := f.svc.PublishPlan(ctx, billing.PlanVersion{}, "a reason"); return e }(),
		"bad feature":       func() error { _, e := f.svc.SaveFeature(ctx, billing.Feature{Key: "X"}, "a reason"); return e }(),
		"retire, no reason": f.svc.RetirePlan(ctx, code, ""),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = f.svc.Plans(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.cli.Features(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
