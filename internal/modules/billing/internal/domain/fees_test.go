package domain_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
)

func i64(v int64) *int64 { return &v }

func TestFee(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rule domain.FeeRule
		base int64
		want int64
	}{
		{"2.5% of 1000.00", domain.FeeRule{RateBps: 250}, 100000, 2500},
		{"half a minor unit rounds up", domain.FeeRule{RateBps: 250}, 20, 1},
		{"below half rounds down", domain.FeeRule{RateBps: 250}, 19, 0},
		{"the minimum", domain.FeeRule{RateBps: 250, MinMinor: 500}, 1000, 500},
		{"never more than the order", domain.FeeRule{RateBps: 250, MinMinor: 500}, 300, 300},
		{"the maximum", domain.FeeRule{RateBps: 250, MaxMinor: i64(1000)}, 1000000, 1000},
		{"nothing sold, no fee", domain.FeeRule{RateBps: 250, MinMinor: 500}, 0, 0},
		{"a zero rate", domain.FeeRule{}, 100000, 0},
		{"100%", domain.FeeRule{RateBps: 10000}, 12345, 12345},
		{"a large order does not overflow", domain.FeeRule{RateBps: 250}, 1_000_000_000_000, 25_000_000_000},
	}
	for _, tt := range tests {
		if got := tt.rule.Fee(tt.base); got != tt.want {
			t.Errorf("%s: Fee(%d) = %d, want %d", tt.name, tt.base, got, tt.want)
		}
	}
}

func TestPeriodOf(t *testing.T) {
	t.Parallel()
	cairo := time.FixedZone("EET", 2*3600)
	if got := domain.PeriodOf(time.Date(2026, 11, 1, 1, 0, 0, 0, cairo)); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("PeriodOf = %v (months are UTC, as quotas are)", got)
	}
}

func TestSellFeeReturned(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		from, want domain.FeeStatus
		changed    bool
	}{
		{domain.FeeDue, domain.FeeCancelled, true},
		{domain.FeeInvoiced, domain.FeeCreditDue, true},
		{domain.FeeCancelled, domain.FeeCancelled, false},
		{domain.FeeCreditDue, domain.FeeCreditDue, false},
		{domain.FeeCredited, domain.FeeCredited, false},
	}
	for _, tt := range tests {
		f, changed := domain.SellFee{Status: tt.from}.Returned(at)
		if f.Status != tt.want || changed != tt.changed || (changed && !f.ReturnedAt.Equal(at)) {
			t.Errorf("Returned from %s = %s, %v", tt.from, f.Status, changed)
		}
	}
	for _, s := range []string{"due", "cancelled", "invoiced", "credit_due", "credited"} {
		if st, err := domain.ParseFeeStatus(s); err != nil || string(st) != s {
			t.Errorf("ParseFeeStatus(%q) = %q, %v", s, st, err)
		}
	}
	if _, err := domain.ParseFeeStatus("paid"); err == nil {
		t.Error("an unknown status parsed")
	}
}
