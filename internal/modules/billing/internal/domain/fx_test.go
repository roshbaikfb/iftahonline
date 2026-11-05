package domain_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func TestFXRateValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rate domain.FXRate
		ok   bool
	}{
		{"the riyal", domain.FXRate{Currency: "SAR", PerUSDMicros: 3_750_000}, true},
		{"the pound", domain.FXRate{Currency: "EGP", PerUSDMicros: 50_000_000}, true},
		{"a million to the dollar", domain.FXRate{Currency: "EGP", PerUSDMicros: 1_000_000 * money.RateScale}, true},
		{"more than a million", domain.FXRate{Currency: "EGP", PerUSDMicros: 1_000_000*money.RateScale + 1}, false},
		{"the dollar itself", domain.FXRate{Currency: "USD", PerUSDMicros: money.RateScale}, false},
		{"an unknown currency", domain.FXRate{Currency: "GBP", PerUSDMicros: 800_000}, false},
		{"zero", domain.FXRate{Currency: "AED", PerUSDMicros: 0}, false},
		{"negative", domain.FXRate{Currency: "AED", PerUSDMicros: -1}, false},
	}
	for _, tt := range tests {
		err := tt.rate.Validate()
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if err != nil && !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: err = %v, want a validation error", tt.name, err)
		}
	}
}

func TestRateBetween(t *testing.T) {
	t.Parallel()
	rates := []domain.FXRate{{Currency: "SAR", PerUSDMicros: 3_750_000}, {Currency: "AED", PerUSDMicros: 3_672_500}}
	tests := []struct {
		name     string
		from, to string
		want     int64
		ok       bool
	}{
		{"the same currency at par", "EGP", "EGP", money.RateScale, true},
		{"the riyal to the dollar", "SAR", "USD", 3_750_000, true},
		{"the dollar to the riyal", "USD", "SAR", 266_667, true},
		{"the riyal to the dirham", "SAR", "AED", 1_021_103, true},
		{"no rate for the pound", "EGP", "USD", 0, false},
		{"no rate to the pound", "USD", "EGP", 0, false},
	}
	for _, tt := range tests {
		got, ok := domain.RateBetween(rates, tt.from, tt.to)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: RateBetween = %d, %v; want %d, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCharge(t *testing.T) {
	t.Parallel()
	rule := domain.FeeRule{RateBps: 100, FixedMinor: 4}
	riyals, err := money.New(37500, money.SAR) // 375.00 SAR
	if err != nil {
		t.Fatal(err)
	}
	conv, fee, err := rule.Charge(riyals, "USD", 3_750_000)
	if err != nil || conv != 10000 || fee != 104 {
		t.Fatalf("Charge = %d, %d, %v; want 100.00 USD and a fee of 1.04", conv, fee, err)
	}
	if _, _, err = rule.Charge(riyals, "USD", 0); !errors.Is(err, money.ErrInvalidRate) {
		t.Fatalf("Charge at no rate: err = %v", err)
	}
}
