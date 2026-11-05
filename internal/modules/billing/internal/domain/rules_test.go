package domain_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestPaymentRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r    domain.PaymentRule
		ok   bool
	}{
		{"Egypt", domain.PaymentRule{Country: "EG", BillingGateway: "paymob", StoreGateways: []string{"paymob"}}, true},
		{"the others", domain.PaymentRule{Country: "ZZ", BillingGateway: "stripe", StoreGateways: []string{"stripe"}}, true},
		{"cash only", domain.PaymentRule{Country: "KW", BillingGateway: "stripe"}, true},
		{"both", domain.PaymentRule{Country: "AE", BillingGateway: "stripe", StoreGateways: []string{"stripe", "paymob"}}, true},
		{"lower case", domain.PaymentRule{Country: "eg", BillingGateway: "paymob"}, false},
		{"an unknown gateway", domain.PaymentRule{Country: "SA", BillingGateway: "tap"}, false},
		{"an unknown store gateway", domain.PaymentRule{Country: "SA", BillingGateway: "stripe", StoreGateways: []string{"moyasar"}}, false},
		{"twice", domain.PaymentRule{Country: "SA", BillingGateway: "stripe", StoreGateways: []string{"stripe", "stripe"}}, false},
	}
	for _, tt := range tests {
		err := tt.r.Validate()
		if (err == nil) != tt.ok || (err != nil && !errs.Is(err, errs.KindValidation)) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}

	rules := []domain.PaymentRule{
		{Country: "EG", BillingGateway: "paymob", StoreGateways: []string{"paymob"}},
		{Country: "ZZ", BillingGateway: "stripe", StoreGateways: []string{"stripe"}},
	}
	if r, ok := domain.RuleFor(rules, "EG"); !ok || r.BillingGateway != "paymob" {
		t.Errorf("EG = %+v", r)
	}
	if r, ok := domain.RuleFor(rules, "SA"); !ok || r.BillingGateway != "stripe" || r.Country != "SA" {
		t.Errorf("SA = %+v", r)
	}
	if _, ok := domain.RuleFor(rules[:1], "SA"); ok {
		t.Error("no rule for the others")
	}
}

func TestTaxes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		t    domain.Tax
		ok   bool
	}{
		{"Saudi VAT", domain.Tax{Country: "SA", RateBps: 1500, NameAr: "ضريبة القيمة المضافة", NameEn: "VAT"}, true},
		{"none", domain.Tax{Country: "ZZ", RateBps: 0, NameAr: "الضريبة", NameEn: "Tax"}, true},
		{"negative", domain.Tax{Country: "SA", RateBps: -1, NameAr: "ض", NameEn: "T"}, false},
		{"over 50%", domain.Tax{Country: "SA", RateBps: 5001, NameAr: "ض", NameEn: "T"}, false},
		{"no name", domain.Tax{Country: "SA", RateBps: 1500, NameAr: " ", NameEn: "VAT"}, false},
		{"a long name", domain.Tax{Country: "SA", RateBps: 1500, NameAr: "ض", NameEn: strings.Repeat("x", 61)}, false},
		{"no country", domain.Tax{Country: "", RateBps: 1500, NameAr: "ض", NameEn: "T"}, false},
	}
	for _, tt := range tests {
		if err := tt.t.Validate(); (err == nil) != tt.ok {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}
