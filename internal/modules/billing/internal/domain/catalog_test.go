package domain_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func fieldsOf(err error) []string {
	var out []string
	if e, ok := errors.AsType[*errs.Error](err); ok {
		for _, f := range e.Fields {
			out = append(out, f.Field)
		}
	}
	return out
}

func TestFeatureValidate(t *testing.T) {
	t.Parallel()
	ok := domain.Feature{Key: "live_chat", Kind: domain.FeatureFlag, NameAr: "دردشة", NameEn: "Live chat"}
	tests := []struct {
		name   string
		mod    func(*domain.Feature)
		fields []string
	}{
		{"valid", func(*domain.Feature) {}, nil},
		{"bad key", func(f *domain.Feature) { f.Key = "Live-Chat" }, []string{"key"}},
		{"bad kind", func(f *domain.Feature) { f.Kind = "toggle" }, []string{"kind"}},
		{"no names", func(f *domain.Feature) { f.NameAr, f.NameEn = " ", "" }, []string{"name_ar", "name_en"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := ok
			tt.mod(&f)
			if got := fieldsOf(f.Validate()); !slices.Equal(got, tt.fields) {
				t.Fatalf("fields = %v, want %v", got, tt.fields)
			}
		})
	}
}

func monthly(amount int64) []domain.Price {
	return []domain.Price{
		{Currency: "EGP", Cycle: domain.Monthly, AmountMinor: amount},
		{Currency: "SAR", Cycle: domain.Monthly, AmountMinor: amount},
		{Currency: "AED", Cycle: domain.Monthly, AmountMinor: amount},
	}
}

func fees() []domain.CurrencyFee {
	return []domain.CurrencyFee{
		{Currency: "EGP", FeeRule: domain.FeeRule{RateBps: 250}},
		{Currency: "SAR", FeeRule: domain.FeeRule{RateBps: 250}},
		{Currency: "AED", FeeRule: domain.FeeRule{RateBps: 250, MinMinor: 100, MaxMinor: i64(1000)}},
	}
}

func TestPlanVersionValidate(t *testing.T) {
	t.Parallel()
	paid := domain.PlanVersion{
		Plan:   domain.Plan{Code: "growth", Kind: domain.KindSubscription, NameAr: "نمو", NameEn: "Growth", TrialDays: 14},
		Prices: append(monthly(9900), domain.Price{Currency: "EGP", Cycle: domain.Annual, AmountMinor: 99000}),
		Grants: domain.Grants{"products": {Value: 100}, "coupons": {Value: 1}, "staff": {Unlimited: true}},
	}
	tests := []struct {
		name   string
		mod    func(*domain.PlanVersion)
		fields []string
	}{
		{"valid paid plan", func(*domain.PlanVersion) {}, nil},
		{"valid free plan", func(v *domain.PlanVersion) { v.Kind, v.TrialDays, v.Prices = domain.KindFree, 0, nil }, nil},
		{"valid pay as you sell", func(v *domain.PlanVersion) {
			v.Kind, v.TrialDays, v.Prices, v.Fees = domain.KindPays, 0, monthly(0), fees()
		}, nil},
		{"bad code and kind", func(v *domain.PlanVersion) { v.Code, v.Kind = "G", "gold" }, []string{"code", "kind"}},
		{"trial on free", func(v *domain.PlanVersion) { v.Kind, v.Prices = domain.KindFree, nil }, []string{"trial_days"}},
		{"trial too long", func(v *domain.PlanVersion) { v.TrialDays = 91 }, []string{"trial_days"}},
		{"missing a currency", func(v *domain.PlanVersion) { v.Prices = v.Prices[1:] }, []string{"prices"}},
		{"duplicate price", func(v *domain.PlanVersion) { v.Prices = append(v.Prices, v.Prices[0]) }, []string{"prices[4]"}},
		{"bad price", func(v *domain.PlanVersion) {
			v.Prices = append(v.Prices, domain.Price{Currency: "USD", Cycle: domain.Monthly, AmountMinor: 1})
		}, []string{"prices[4]"}},
		{"free plan with a price", func(v *domain.PlanVersion) { v.Kind, v.TrialDays = domain.KindFree, 0 }, []string{"prices"}},
		{"pays with a price", func(v *domain.PlanVersion) { v.Kind, v.TrialDays, v.Fees = domain.KindPays, 0, fees() }, []string{"prices[0]", "prices[1]", "prices[2]", "prices[3]"}},
		{"pays without a fee", func(v *domain.PlanVersion) { v.Kind, v.TrialDays, v.Prices = domain.KindPays, 0, nil }, []string{"fees", "fees", "fees"}},
		{"fees on a paid plan", func(v *domain.PlanVersion) { v.Fees = fees() }, []string{"fees"}},
		{"bad fee", func(v *domain.PlanVersion) {
			v.Kind, v.TrialDays, v.Prices, v.Fees = domain.KindPays, 0, nil, fees()
			v.Fees[2].MaxMinor = i64(10)
		}, []string{"fees[2]"}},
		{"negative grant", func(v *domain.PlanVersion) { v.Grants = domain.Grants{"products": {Value: -1}} }, []string{"grants.products"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := paid
			v.Prices = slices.Clone(paid.Prices)
			tt.mod(&v)
			if got := fieldsOf(v.Validate()); !slices.Equal(got, tt.fields) {
				t.Fatalf("fields = %v, want %v", got, tt.fields)
			}
		})
	}
}
