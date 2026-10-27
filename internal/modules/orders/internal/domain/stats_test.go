package domain_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
)

func TestStatsWindows(t *testing.T) {
	t.Parallel()
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 15, 30, 0, 0, cairo)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, cairo) }
	cases := []struct {
		r                      domain.StatsRange
		from, prevFrom, prevTo time.Time
	}{
		// today until now, against yesterday until the same hour
		{domain.RangeToday, at(10, 27, 0), at(10, 26, 0), now.AddDate(0, 0, -1)},
		{domain.Range7d, at(10, 21, 0), at(10, 14, 0), now.AddDate(0, 0, -7)},
		{domain.Range30d, at(9, 28, 0), at(8, 29, 0), now.AddDate(0, 0, -30)},
	}
	for _, c := range cases {
		w := c.r.Windows(now, cairo)
		if !w.From.Equal(c.from) || !w.To.Equal(now) || !w.PrevFrom.Equal(c.prevFrom) || !w.PrevTo.Equal(c.prevTo) {
			t.Errorf("%s: from %s to %s, previous %s to %s; want from %s, previous %s to %s", c.r, w.From, w.To, w.PrevFrom, w.PrevTo, c.from, c.prevFrom, c.prevTo)
		}
		if !w.ChartFrom.Equal(at(9, 28, 0)) {
			t.Errorf("%s: chart from %s", c.r, w.ChartFrom)
		}
	}
}

func TestStatsWindowsAcrossDST(t *testing.T) {
	t.Parallel()
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, cairo)
	w := domain.Range7d.Windows(now, cairo)
	if got := w.From.In(cairo); got.Hour() != 0 || got.Day() != 20 {
		t.Fatalf("7 days back = %s; want local midnight of the 20th", got)
	}
	days := domain.FillDays(w.ChartFrom, cairo, 30, nil)
	if len(days) != 30 || days[0].Date != w.ChartFrom.In(cairo).Format(time.DateOnly) || days[29].Date != "2026-04-26" {
		t.Fatalf("days %v .. %v", days[0], days[len(days)-1])
	}
}

func TestFillDays(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	got := domain.FillDays(start, time.UTC, 3, map[string]domain.Day{"2026-10-26": {Date: "2026-10-26", SalesMinor: 500, Orders: 2}})
	want := []domain.Day{{Date: "2026-10-25"}, {Date: "2026-10-26", SalesMinor: 500, Orders: 2}, {Date: "2026-10-27"}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("day %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestParseStatsRange(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"today", "7d", "30d"} {
		if r, ok := domain.ParseStatsRange(s); !ok || string(r) != s {
			t.Errorf("%q: %q %v", s, r, ok)
		}
	}
	for _, s := range []string{"", "1y", "TODAY"} {
		if _, ok := domain.ParseStatsRange(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}
