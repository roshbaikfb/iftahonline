package domain

import (
	"cmp"
	"slices"
	"time"
)

// The console's growth page (ADR-0081): the last 12 Cairo months, each
// figure laid over every one of them.

// GrowthMonths is how many months the page shows, this one included.
const GrowthMonths = 12

// ChurnGraceDays is how late in a month its paying accounts may be read
// and still count as its start: a worker down on the 1st is caught up.
const ChurnGraceDays = 3

// GrowthWindow is the page's months, each its first day at midnight UTC,
// oldest first; Since is the instant the first one starts in the console's
// zone, and Today the zone's date.
type GrowthWindow struct {
	Months []time.Time
	Since  time.Time
	Today  time.Time
}

// GrowthWindowAt is the window at now in loc.
func GrowthWindowAt(now time.Time, loc *time.Location) GrowthWindow {
	local := now.In(loc)
	first := time.Date(local.Year(), local.Month()-GrowthMonths+1, 1, 0, 0, 0, 0, loc)
	w := GrowthWindow{Since: first.UTC(), Today: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)}
	for i := range GrowthMonths {
		m := first.AddDate(0, i, 0)
		w.Months = append(w.Months, time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, time.UTC))
	}
	return w
}

// ThisMonth is the window's last month, the one under way.
func (w GrowthWindow) ThisMonth() time.Time { return w.Months[len(w.Months)-1] }

// MonthCount is the stores created in a month and those that paid.
type MonthCount struct {
	Month              time.Time
	Created, Converted int64
}

// FillStores lays counts over every month of the window, a missing month
// as zero.
func (w GrowthWindow) FillStores(counts []MonthCount) []MonthCount {
	out := make([]MonthCount, len(w.Months))
	for i, m := range w.Months {
		out[i] = MonthCount{Month: m}
		for _, c := range counts {
			if c.Month.Equal(m) {
				out[i] = c
			}
		}
	}
	return out
}

// MonthAmount is an amount in one month and currency.
type MonthAmount struct {
	Month       time.Time
	Currency    string
	Invoices    int64
	AmountMinor int64
}

// CurrencyMonths are one currency's amounts over the window.
type CurrencyMonths struct {
	Currency string
	Months   []MonthAmount
}

// currencyOrder is the order the console lists currencies in.
func currencyOrder(c string) int {
	if i := slices.Index([]string{"EGP", "SAR", "AED"}, c); i >= 0 {
		return i
	}
	return 3
}

// FillCollected lays amounts over every month of the window, one series
// per currency that has any, EGP then SAR then AED.
func (w GrowthWindow) FillCollected(amounts []MonthAmount) []CurrencyMonths {
	var currencies []string
	for _, a := range amounts {
		if !slices.Contains(currencies, a.Currency) {
			currencies = append(currencies, a.Currency)
		}
	}
	slices.SortFunc(currencies, func(a, b string) int {
		return cmp.Or(cmp.Compare(currencyOrder(a), currencyOrder(b)), cmp.Compare(a, b))
	})
	out := make([]CurrencyMonths, 0, len(currencies))
	for _, c := range currencies {
		series := CurrencyMonths{Currency: c, Months: make([]MonthAmount, len(w.Months))}
		for i, m := range w.Months {
			series.Months[i] = MonthAmount{Month: m, Currency: c}
			for _, a := range amounts {
				if a.Currency == c && a.Month.Equal(m) {
					series.Months[i] = a
				}
			}
		}
		out = append(out, series)
	}
	return out
}

// MonthChurn is a month's paying accounts: those paying as it began,
// those that joined by the next month's start, and those lost by then.
type MonthChurn struct {
	Month               time.Time
	Start, Joined, Lost int64
}

// RateBps is the share of the month's starting accounts lost by its end,
// in basis points, rounded half up.
func (c MonthChurn) RateBps() int64 {
	if c.Start == 0 {
		return 0
	}
	return (c.Lost*10000*2 + c.Start) / (c.Start * 2)
}
