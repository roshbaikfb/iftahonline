package app_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestPaymentRulesAndTaxes: the approved rules are seeded (Egypt through
// Paymob, the others through Stripe); the console gives a country its
// own rule and tax and removes them, never the others' row; merchants'
// code cannot change them (ADR-0089). Kuwait and Qatar only: other tests
// read Egypt's and the others'.
func TestPaymentRulesAndTaxes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})
	t.Cleanup(func() {
		for _, c := range []string{"KW", "QA"} {
			_ = console.RemovePaymentRule(context.Background(), c)
			_ = console.RemoveTax(context.Background(), c)
		}
	})

	eg, err := f.svc.PaymentRuleFor(t.Context(), "EG")
	if err != nil || eg.BillingGateway != "paymob" || len(eg.StoreGateways) != 1 || eg.StoreGateways[0] != "paymob" {
		t.Fatalf("Egypt = %+v, %v", eg, err)
	}
	kw, err := f.svc.PaymentRuleFor(t.Context(), "KW")
	if err != nil || kw.Country != "KW" || kw.BillingGateway != "stripe" || kw.StoreGateways[0] != "stripe" {
		t.Fatalf("Kuwait by the others' rule = %+v, %v", kw, err)
	}

	cashOnly := domain.PaymentRule{Country: "KW", BillingGateway: "stripe", StoreGateways: nil}
	if err = f.svc.SavePaymentRule(t.Context(), cashOnly); err == nil {
		t.Fatal("the merchants' role changed a rule")
	}
	if err = console.SavePaymentRule(t.Context(), domain.PaymentRule{Country: "KW", BillingGateway: "tap"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an unknown gateway: %v", err)
	}
	if err = console.SavePaymentRule(t.Context(), cashOnly); err != nil {
		t.Fatal(err)
	}
	if kw, err = f.svc.PaymentRuleFor(t.Context(), "KW"); err != nil || len(kw.StoreGateways) != 0 {
		t.Fatalf("Kuwait's own = %+v, %v", kw, err)
	}
	rules, err := console.PaymentRules(t.Context())
	if err != nil || len(rules) < 3 || rules[len(rules)-1].Country != domain.OtherCountries {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	if err = console.RemovePaymentRule(t.Context(), "KW"); err != nil {
		t.Fatal(err)
	}
	if err = console.RemovePaymentRule(t.Context(), "KW"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
	if err = console.RemovePaymentRule(t.Context(), domain.OtherCountries); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("remove the others': %v", err)
	}

	// Taxes: the seeded three and the others' (0); Qatar gets its own.
	taxes, err := console.Taxes(t.Context())
	if err != nil || taxes[len(taxes)-1].Country != domain.OtherCountries || taxes[len(taxes)-1].RateBps != 0 {
		t.Fatalf("taxes = %+v, %v", taxes, err)
	}
	if err = console.SaveTax(t.Context(), domain.Tax{Country: "QA", RateBps: 6000, NameAr: "ض", NameEn: "T"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("60%%: %v", err)
	}
	if err = console.SaveTax(t.Context(), domain.Tax{Country: "QA", RateBps: 500, NameAr: " ضريبة القيمة المضافة ", NameEn: "VAT"}); err != nil {
		t.Fatal(err)
	}
	found := false
	if taxes, err = f.svc.Taxes(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, x := range taxes {
		found = found || (x.Country == "QA" && x.RateBps == 500 && x.NameAr == "ضريبة القيمة المضافة")
	}
	if !found {
		t.Fatalf("Qatar's tax not saved: %+v", taxes)
	}
	if err = f.svc.SaveTax(t.Context(), domain.Tax{Country: "QA", RateBps: 0, NameAr: "ض", NameEn: "T"}); err == nil {
		t.Fatal("the merchants' role changed a tax")
	}
	if err = console.RemoveTax(t.Context(), "QA"); err != nil {
		t.Fatal(err)
	}
	if err = console.RemoveTax(t.Context(), domain.OtherCountries); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("remove the others' tax: %v", err)
	}
}
