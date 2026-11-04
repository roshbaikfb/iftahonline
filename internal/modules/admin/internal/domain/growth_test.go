package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
)

func cairo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// TestGrowthWindow (ADR-0081): the last 12 Cairo months, this one
// included, start where Cairo's month starts, even when UTC is still in
// the month before.
func TestGrowthWindow(t *testing.T) {
	t.Parallel()
	loc := cairo(t)
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	for _, tt := range []struct {
		name        string
		now         time.Time
		first, last time.Time
		since       time.Time
		today       time.Time
	}{
		{"mid-month", time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC), month(2025, 12), month(2026, 11),
			time.Date(2025, 11, 30, 22, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)},
		{"Cairo's 1 December, still November in UTC", time.Date(2026, 11, 30, 22, 30, 0, 0, time.UTC), month(2026, 1), month(2026, 12),
			time.Date(2025, 12, 31, 22, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
	} {
		w := domain.GrowthWindowAt(tt.now, loc)
		if len(w.Months) != domain.GrowthMonths || !w.Months[0].Equal(tt.first) || !w.Months[len(w.Months)-1].Equal(tt.last) {
			t.Errorf("%s: months %v … %v", tt.name, w.Months[0], w.Months[len(w.Months)-1])
		}
		if !w.Since.Equal(tt.since) || !w.Today.Equal(tt.today) || !w.ThisMonth().Equal(tt.last) {
			t.Errorf("%s: since %v, today %v", tt.name, w.Since, w.Today)
		}
	}
}

// TestFillMonths: a month with nothing is a zero, a month outside the
// window is dropped, and each currency is laid over every month.
func TestFillMonths(t *testing.T) {
	t.Parallel()
	w := domain.GrowthWindowAt(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), cairo(t))
	m := func(mo time.Month, y int) time.Time { return time.Date(y, mo, 1, 0, 0, 0, 0, time.UTC) }

	stores := w.FillStores([]domain.MonthCount{{Month: m(1, 2026), Created: 5, Converted: 2}, {Month: m(1, 2020), Created: 9}})
	if len(stores) != domain.GrowthMonths || stores[9] != (domain.MonthCount{Month: m(1, 2026), Created: 5, Converted: 2}) || stores[11] != (domain.MonthCount{Month: m(3, 2026)}) {
		t.Fatalf("stores = %+v", stores)
	}

	got := w.FillCollected([]domain.MonthAmount{
		{Month: m(2, 2026), Currency: "SAR", Invoices: 1, AmountMinor: 9900},
		{Month: m(3, 2026), Currency: "EGP", Invoices: 2, AmountMinor: 59800},
	})
	if len(got) != 2 || got[0].Currency != "EGP" || got[1].Currency != "SAR" {
		t.Fatalf("currencies = %+v", got)
	}
	egp := got[0].Months
	if len(egp) != domain.GrowthMonths || egp[11] != (domain.MonthAmount{Month: m(3, 2026), Currency: "EGP", Invoices: 2, AmountMinor: 59800}) || egp[10].AmountMinor != 0 {
		t.Fatalf("EGP = %+v", egp)
	}
	if got[1].Months[10].AmountMinor != 9900 {
		t.Fatalf("SAR = %+v", got[1].Months)
	}
	if none := w.FillCollected(nil); len(none) != 0 {
		t.Fatalf("none = %+v", none)
	}
}

// TestChurnRate: the share of the accounts paying as the month began that
// were gone by the next, in basis points; none when none paid.
func TestChurnRate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		c    domain.MonthChurn
		want int64
	}{
		{domain.MonthChurn{Start: 200, Lost: 7}, 350},
		{domain.MonthChurn{Start: 3, Lost: 1}, 3333},
		{domain.MonthChurn{}, 0},
	} {
		if got := tt.c.RateBps(); got != tt.want {
			t.Errorf("%+v = %d; want %d", tt.c, got, tt.want)
		}
	}
	if !reflect.DeepEqual(domain.MonthChurn{Start: 1}, domain.MonthChurn{Start: 1}) {
		t.Fatal("comparable")
	}
}
