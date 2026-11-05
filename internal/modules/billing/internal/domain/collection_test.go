package domain_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestCollectionSettings(t *testing.T) {
	t.Parallel()
	ok := map[string]int64{"EGP": 30000, "SAR": 10000, "AED": 10000, "USD": 1000}
	tests := []struct {
		name     string
		minimums map[string]int64
		valid    bool
	}{
		{"the approved defaults", ok, true},
		{"the bounds", map[string]int64{"EGP": 100, "SAR": 100_000_000, "AED": 100, "USD": 100}, true},
		{"below 1.00", map[string]int64{"EGP": 99, "SAR": 10000, "AED": 10000, "USD": 1000}, false},
		{"above 1,000,000", map[string]int64{"EGP": 30000, "SAR": 100_000_001, "AED": 10000, "USD": 1000}, false},
		{"a currency missing", map[string]int64{"EGP": 30000, "SAR": 10000, "AED": 10000}, false},
		{"an unknown currency", map[string]int64{"EGP": 30000, "SAR": 10000, "AED": 10000, "USD": 1000, "GBP": 10000}, false},
		{"none", nil, false},
	}
	for _, tt := range tests {
		err := domain.CollectionSettings{Minimums: tt.minimums}.Validate()
		if (err == nil) != tt.valid {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		var e *errs.Error
		if err != nil && (!errors.As(err, &e) || e.Kind != errs.KindValidation) {
			t.Errorf("%s: not a validation error: %v", tt.name, err)
		}
	}
	merged := domain.CollectionSettings{Minimums: ok}.Merge(true, false, map[string]int64{"USD": 2000})
	if !merged.Prepaid || merged.Card || merged.Minimums["USD"] != 2000 || merged.Minimums["EGP"] != 30000 || ok["USD"] != 1000 {
		t.Errorf("Merge = %+v (the others kept, the original untouched)", merged)
	}
	if m := (domain.CollectionSettings{}).Merge(false, false, map[string]int64{"EGP": 100}); m.Minimums["EGP"] != 100 {
		t.Errorf("Merge on nothing = %+v", m)
	}
	offered := []struct {
		prepaid, card bool
		want          []domain.Collection
	}{
		{false, false, nil},
		{true, false, []domain.Collection{domain.CollectBalance}},
		{false, true, []domain.Collection{domain.CollectCard}},
		{true, true, []domain.Collection{domain.CollectBalance, domain.CollectCard}},
	}
	for _, tt := range offered {
		if got := (domain.CollectionSettings{Prepaid: tt.prepaid, Card: tt.card}).Offered(); !slices.Equal(got, tt.want) {
			t.Errorf("prepaid %v card %v: offered %v, want %v", tt.prepaid, tt.card, got, tt.want)
		}
	}
}

func TestLevelOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		balance int64
		want    domain.BalanceLevel
	}{
		{30000, domain.BalanceOK},
		{15000, domain.BalanceOK}, // half the minimum is still enough
		{14999, domain.BalanceLow},
		{1, domain.BalanceLow},
		{0, domain.BalanceDebt},
		{-4230, domain.BalanceDebt},
	}
	for _, tt := range tests {
		if got := domain.LevelOf(tt.balance, 30000); got != tt.want {
			t.Errorf("LevelOf(%d) = %s, want %s", tt.balance, got, tt.want)
		}
	}
}

func TestParseCredit(t *testing.T) {
	t.Parallel()
	c, err := domain.ParseCredit(50000, "transfer", "  CIB 7741 2209 ")
	if err != nil || c.AmountMinor != 50000 || c.Reason != domain.ReasonTransfer || c.Reference != "CIB 7741 2209" {
		t.Fatalf("credit = %+v, %v", c, err)
	}
	long := make([]byte, 121)
	for i := range long {
		long[i] = 'x'
	}
	bad := []struct {
		name      string
		amount    int64
		reason    string
		reference string
	}{
		{"nothing", 0, "transfer", "r"},
		{"negative", -100, "transfer", "r"},
		{"over 1,000,000", 100_000_001, "transfer", "r"},
		{"no reason", 100, "", "r"},
		{"an unknown reason", 100, "gift", "r"},
		{"no reference", 100, "correction", "   "},
		{"a long reference", 100, "compensation", string(long)},
	}
	for _, tt := range bad {
		if _, err := domain.ParseCredit(tt.amount, tt.reason, tt.reference); err == nil {
			t.Errorf("%s: want an error", tt.name)
		}
	}
}

func TestBillingCurrencies(t *testing.T) {
	t.Parallel()
	for country, want := range map[string]string{"EG": "EGP", "SA": "USD", "AE": "USD", "KW": "USD", "GB": "USD"} {
		if got := domain.BillingCurrency(country); got != want {
			t.Errorf("BillingCurrency(%s) = %s, want %s", country, got, want)
		}
	}
	for cur, want := range map[string][2]bool{"EGP": {true, true}, "USD": {true, true}, "SAR": {true, false}, "AED": {true, false}, "GBP": {false, false}} {
		if got := [2]bool{domain.Billed(cur), domain.OnSale(cur)}; got != want {
			t.Errorf("%s: billed, on sale = %v, want %v", cur, got, want)
		}
	}
}
