package app_test

import (
	"slices"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestGrowth (ADR-0081): the hourly reading keeps each Cairo day's latest
// revenue and each month's first paying accounts; churn compares months
// read in their first days only; the page reads every section, and names
// those it could not. Its rows are dated in 2031, where nothing else is.
func TestGrowth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	svc := app.NewService(f.deps)
	at := func(s string) func() time.Time {
		return func() time.Time {
			v, err := time.Parse(time.RFC3339, s)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	// 00:30 on 1 March in Cairo, still February in UTC; then again later.
	for _, now := range []string{"2031-02-28T22:30:00Z", "2031-03-01T08:00:00Z"} {
		svc.SetNow(at(now))
		if err := svc.SnapshotGrowth(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	mrr, err := store.MRRSince(t.Context(), f.deps.DB, day(2031, 3, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(mrr) != 3 || !mrr[0].Day.Equal(day(2031, 3, 1)) || mrr[0].Currency != "AED" || mrr[2].Currency != "SAR" {
		t.Fatalf("one reading a day per currency = %+v", mrr)
	}

	// Who paid as April and May began, and June read too late to count.
	gen := ulid.NewGenerator()
	a, b, c, d, e := gen.New(), gen.New(), gen.New(), gen.New(), gen.New()
	for _, m := range []struct {
		month, on time.Time
		accounts  []ulid.ID
		first     bool
	}{
		{day(2031, 4, 1), day(2031, 4, 3), []ulid.ID{a, b, c}, true},
		{day(2031, 4, 1), day(2031, 4, 4), []ulid.ID{a}, false}, // the month keeps its first reading
		{day(2031, 5, 1), day(2031, 5, 1), []ulid.ID{b, c, d, e}, true},
		{day(2031, 6, 1), day(2031, 6, 1+domain.ChurnGraceDays), nil, true},
	} {
		first, startErr := store.StartMonth(t.Context(), f.deps.DB, m.month, m.on, m.accounts)
		if startErr != nil || first != m.first {
			t.Fatalf("%v: first = %v, %v", m.month, first, startErr)
		}
	}

	svc.SetNow(at("2031-06-15T12:00:00Z"))
	g, err := svc.Growth(ctx)
	if err != nil || len(g.Errors) != 0 {
		t.Fatalf("growth = %v, %v", g.Errors, err)
	}
	if len(g.Months) != domain.GrowthMonths || !g.Months[0].Equal(day(2030, 7, 1)) || !g.Months[11].Equal(day(2031, 6, 1)) {
		t.Fatalf("months = %v … %v", g.Months[0], g.Months[len(g.Months)-1])
	}
	months := func() []time.Time {
		out := []time.Time{}
		for _, m := range g.Churn {
			out = append(out, m.Month)
		}
		return out
	}()
	// March (read on its 1st) to April (its 3rd); April to May; not May,
	// whose next month was read late.
	if !slices.Equal(months, []time.Time{day(2031, 3, 1), day(2031, 4, 1)}) {
		t.Fatalf("churn months = %v", months)
	}
	if april := g.Churn[1]; april.Start != 3 || april.Joined != 2 || april.Lost != 1 || april.RateBps() != 3333 {
		t.Fatalf("april = %+v", april)
	}
	if !slices.ContainsFunc(g.MRR, func(m store.DayMRR) bool { return m.Day.Equal(day(2031, 3, 1)) && m.Currency == "EGP" }) {
		t.Fatalf("mrr = %+v", g.MRR)
	}
	if g.Collected == nil || len(g.Stores) != domain.GrowthMonths || g.Plans == nil || g.Paying == nil {
		t.Fatalf("sections = %+v", g)
	}

	if _, err = svc.Growth(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	bare := f.withoutBilling()
	bare.SetNow(at("2031-06-15T12:00:00Z"))
	g, err = bare.Growth(ctx)
	if err != nil || !slices.Equal(g.Errors, []string{"collected", "stores", "plans"}) || g.Churn == nil || g.Collected != nil {
		t.Fatalf("without billing = %v, %v", g.Errors, err)
	}
	if err = bare.SnapshotGrowth(t.Context()); err == nil {
		t.Fatal("a reading without billing")
	}
}
