package domain

import "time"

// StatsRange is the period the dashboard home sums: today, or the last 7
// or 30 days including today, in the store's time zone.
type StatsRange string

// The ranges.
const (
	RangeToday StatsRange = "today"
	Range7d    StatsRange = "7d"
	Range30d   StatsRange = "30d"
)

// ChartDays is how many days the sales chart shows, today included.
const ChartDays = 30

// ParseStatsRange reads a range.
func ParseStatsRange(s string) (StatsRange, bool) {
	switch r := StatsRange(s); r {
	case RangeToday, Range7d, Range30d:
		return r, true
	}
	return "", false
}

func (r StatsRange) days() int {
	switch r {
	case Range7d:
		return 7
	case Range30d:
		return 30
	}
	return 1
}

// Windows are the instants a range sums between: [From, To) is the period
// up to now; [PrevFrom, PrevTo) is the same span one period earlier (today
// until now is compared with yesterday until the same hour), and
// ChartFrom is the first day of the sales chart.
type Windows struct {
	From, To, PrevFrom, PrevTo, ChartFrom time.Time
}

// Windows places the range at now in loc. Days start at local midnight,
// taken from the calendar, so a day with a clock change is still a day.
func (r StatsRange) Windows(now time.Time, loc *time.Location) Windows {
	local := now.In(loc)
	midnight := func(daysBack int) time.Time {
		return time.Date(local.Year(), local.Month(), local.Day()-daysBack, 0, 0, 0, 0, loc)
	}
	from := midnight(r.days() - 1)
	back := func(t time.Time) time.Time { return t.In(loc).AddDate(0, 0, -r.days()) }
	return Windows{From: from, To: now, PrevFrom: back(from), PrevTo: back(now), ChartFrom: midnight(ChartDays - 1)}
}

// Tally is a number of orders and their total.
type Tally struct {
	Orders     int
	TotalMinor int64
}

// Day is one day of the sales chart, by local date (YYYY-MM-DD).
type Day struct {
	Date       string
	SalesMinor int64
	Orders     int
}

// Stats is the dashboard home's summary of a store's orders. Sales count
// every order placed in the period except cancelled and returned ones,
// which are reported apart (the owner, 2026-10-27); totals include
// shipping and the cash-on-delivery fee.
type Stats struct {
	Range         StatsRange
	Currency      string
	Windows       Windows
	Sales         Tally
	COD, Card     int // orders by payment method, within Sales
	Previous      Tally
	Cancelled     Tally
	Returned      Tally
	Daily         []Day
	OldestPending *time.Time
}

// FillDays lists n days from start (a local midnight in loc), taking each
// day's figures from got and zero for days without orders.
func FillDays(start time.Time, loc *time.Location, n int, got map[string]Day) []Day {
	s := start.In(loc)
	out := make([]Day, n)
	for i := range n {
		date := time.Date(s.Year(), s.Month(), s.Day()+i, 0, 0, 0, 0, loc).Format(time.DateOnly)
		d, ok := got[date]
		if !ok {
			d = Day{Date: date}
		}
		out[i] = d
	}
	return out
}
