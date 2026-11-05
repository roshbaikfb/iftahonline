package app_test

import (
	"context"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestPaymentRulesAndTaxesFromTheConsole: each change needs a reason and
// is recorded first; the others' row stays (ADR-0089). Oman only: other
// tests read the seeded rows.
func TestPaymentRulesAndTaxesFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	t.Cleanup(func() {
		_ = f.svc.RemovePaymentRule(ctx, "OM", "test cleanup")
		_ = f.svc.RemoveTax(ctx, "OM", "test cleanup")
	})

	rules, err := f.svc.PaymentRules(ctx)
	if err != nil || len(rules) < 2 || rules[len(rules)-1].Country != billing.OtherCountries {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	om := billing.PaymentRule{Country: "OM", BillingGateway: "stripe"}
	if err = f.svc.SavePaymentRule(ctx, om, ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if err = f.svc.SavePaymentRule(ctx, billing.PaymentRule{Country: "OM", BillingGateway: "tap"}, "a local gateway"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an unknown gateway: %v", err)
	}
	if err = f.svc.SavePaymentRule(ctx, om, "cash on delivery only for now"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RemovePaymentRule(ctx, "OM", "back to the others' rule"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RemovePaymentRule(ctx, billing.OtherCountries, "remove the others"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("the others' row: %v", err)
	}

	if _, err = f.svc.Taxes(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SaveTax(ctx, billing.Tax{Country: "OM", RateBps: 500, NameAr: "ضريبة", NameEn: "VAT"}, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a short reason: %v", err)
	}
	if err = f.svc.SaveTax(ctx, billing.Tax{Country: "OM", RateBps: 9000, NameAr: "ضريبة", NameEn: "VAT"}, "too much"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("90%%: %v", err)
	}
	if err = f.svc.SaveTax(ctx, billing.Tax{Country: "OM", RateBps: 500, NameAr: "ضريبة القيمة المضافة", NameEn: "VAT"}, "Oman's VAT"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RemoveTax(ctx, "OM", "back to the others' tax"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RemoveTax(ctx, "OM", "again"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("remove twice: %v", err)
	}

	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"billing.payment_rule_saved", "billing.payment_rule_removed", "billing.tax_saved", "billing.tax_removed"} {
		if !slices.ContainsFunc(page.Items, func(e app.AuditItem) bool {
			return e.AdminID != nil && *e.AdminID == me.Admin.ID && e.Action == action && e.TargetID == "OM"
		}) {
			t.Errorf("%s not in the admin log", action)
		}
	}
	if _, err = f.cli.PaymentRules(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}

// TestFXRatesAndMinimumsFromTheConsole: a rate needs a reason and is
// recorded first with the one it replaces; saving the minimums keeps
// the currencies left out (ADR-0090). The pound only: other tests read
// the riyal's and the dirham's seeded rates.
func TestFXRatesAndMinimumsFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)

	rates, err := f.svc.FXRates(ctx)
	if err != nil || len(rates) < 2 {
		t.Fatalf("rates = %+v, %v", rates, err)
	}
	pound := billing.FXRate{Currency: "EGP", PerUSDMicros: 48_000_000}
	if err = f.svc.SaveFXRate(ctx, pound, ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if err = f.svc.SaveFXRate(ctx, billing.FXRate{Currency: "EGP", PerUSDMicros: -1}, "a bad rate"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a negative rate: %v", err)
	}
	for _, r := range []billing.FXRate{pound, {Currency: "EGP", PerUSDMicros: 48_500_000}} {
		if err = f.svc.SaveFXRate(ctx, r, "the bank's rate today"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Items, func(e app.AuditItem) bool {
		return e.AdminID != nil && *e.AdminID == me.Admin.ID && e.Action == "billing.fx_rate_saved" && e.TargetID == "EGP"
	}) {
		t.Error("billing.fx_rate_saved not in the admin log")
	}
	if _, err = f.cli.FXRates(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}

	before, err := f.deps.Collection.CollectionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.deps.Collection.SaveCollectionSettings(context.Background(), before) })
	if _, err = f.svc.SaveCollectionSettings(ctx, false, false, map[string]int64{"USD": 2000}, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a short reason: %v", err)
	}
	if _, err = f.svc.SaveCollectionSettings(ctx, false, false, map[string]int64{"USD": 5}, "too little"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("out of bounds: %v", err)
	}
	got, err := f.svc.SaveCollectionSettings(ctx, before.Prepaid, before.Card, map[string]int64{"USD": 2000}, "the dollar's minimum")
	if err != nil || got.Minimums["USD"] != 2000 || got.Minimums["EGP"] != before.Minimums["EGP"] || got.Minimums["SAR"] != before.Minimums["SAR"] {
		t.Fatalf("saved = %+v, %v", got, err)
	}
	if _, err = f.cli.SaveCollectionSettings(ctx, false, false, map[string]int64{"USD": 2000}, "no collection"); err == nil {
		t.Fatal("saved without billing's collection")
	}
}
