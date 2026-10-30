package domain_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
)

func TestTotals(t *testing.T) {
	t.Parallel()
	sub := domain.Line{Kind: domain.LineSubscription, Quantity: 1, AmountMinor: 59900}
	fees := domain.Line{Kind: domain.LineSellFees, Quantity: 12, AmountMinor: 30001}
	credit := domain.Line{Kind: domain.LineFeeCredit, Quantity: 2, AmountMinor: -5000}
	tests := []struct {
		name                 string
		lines                []domain.Line
		taxBps               int32
		ok                   bool
		subtotal, tax, total int64
	}{
		{"subscription and fees, Egypt's 14%", []domain.Line{sub, fees}, 1400, true, 89901, 12586, 102487},
		{"a credit lowers it", []domain.Line{sub, fees, credit}, 1400, true, 84901, 11886, 96787},
		{"no tax", []domain.Line{fees}, 0, true, 30001, 0, 30001},
		{"nothing to bill", nil, 1400, false, 0, 0, 0},
		{"credits only: nothing is billed, they wait", []domain.Line{credit}, 1400, false, 0, 0, 0},
		{"credits as large as the charges: nothing billed", []domain.Line{{Kind: domain.LineSellFees, Quantity: 1, AmountMinor: 5000}, credit}, 1400, false, 0, 0, 0},
	}
	for _, tt := range tests {
		got, ok := domain.Totals(tt.lines, tt.taxBps)
		if ok != tt.ok || got.SubtotalMinor != tt.subtotal || got.TaxMinor != tt.tax || got.TotalMinor != tt.total {
			t.Errorf("%s: %+v, %v", tt.name, got, ok)
		}
	}
}

func TestInvoiceNumber(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{1: "INV-2026-000001", 42: "INV-2026-000042", 1234567: "INV-2026-1234567"} {
		if got := domain.InvoiceNumber(2026, n); got != want {
			t.Errorf("InvoiceNumber(2026, %d) = %q, want %q", n, got, want)
		}
	}
}

func TestInvoiceDates(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	if p := domain.PreviousPeriod(at); !p.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("PreviousPeriod = %v", p)
	}
	if p := domain.PreviousPeriod(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); !p.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("across a year: %v", p)
	}
	if d := domain.DueAt(at); !d.Equal(at.Add(domain.PaymentTerm)) {
		t.Errorf("DueAt = %v", d)
	}
	if !domain.RenewsBy(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)).Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("an October invoice renews what ends by November 1")
	}
}

func TestCountryOf(t *testing.T) {
	t.Parallel()
	for cur, want := range map[string]string{"EGP": "EG", "SAR": "SA", "AED": "AE", "USD": ""} {
		if got := domain.CountryOf(cur); got != want {
			t.Errorf("CountryOf(%s) = %q", cur, got)
		}
	}
}
