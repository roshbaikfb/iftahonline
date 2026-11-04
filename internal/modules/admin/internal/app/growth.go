package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
)

// The console's growth page (ADR-0081): the last 12 Cairo months from
// billing's invoices and the stores' dates, and from the console's own
// record (an hourly reading of what pays) what billing keeps no history
// of.

// GrowthEvery is how often what pays is read: the day keeps its latest
// reading, and a month's paying accounts are its first.
const GrowthEvery = time.Hour

var errNoZone = errors.New("admin: the console's zone")

// cairo is the zone the console counts days and months in.
func cairo() (*time.Location, error) {
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoZone, err)
	}
	return loc, nil
}

// GrowthSnapshotArgs reads what pays now.
type GrowthSnapshotArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (GrowthSnapshotArgs) Kind() string { return "admin.growth_snapshot" }

// GrowthSnapshotWorker runs GrowthSnapshotArgs.
type GrowthSnapshotWorker struct {
	river.WorkerDefaults[GrowthSnapshotArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *GrowthSnapshotWorker) Work(ctx context.Context, _ *river.Job[GrowthSnapshotArgs]) error {
	return w.Svc.SnapshotGrowth(ctx)
}

// Timeout bounds one reading.
func (*GrowthSnapshotWorker) Timeout(*river.Job[GrowthSnapshotArgs]) time.Duration {
	return time.Minute
}

// GrowthPeriodic schedules the reading.
func GrowthPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(GrowthEvery),
		func() (river.JobArgs, *river.InsertOpts) { return GrowthSnapshotArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

// growthCurrencies are recorded every day, a currency with nothing at 0,
// so a line drops to zero rather than breaking off.
var growthCurrencies = []string{"EGP", "SAR", "AED"} //nolint:gochecknoglobals // a fixed list

// SnapshotGrowth records today's recurring revenue per currency and, on
// the month's first reading, its paying accounts.
func (s *Service) SnapshotGrowth(ctx context.Context) error {
	if s.d.Billing == nil {
		return errNoBilling
	}
	loc, err := cairo()
	if err != nil {
		return err
	}
	today, err := s.d.Billing.Today(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	w := domain.GrowthWindowAt(now, loc)
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		for _, c := range growthCurrencies {
			m := store.DayMRR{Day: w.Today, Currency: c}
			for _, r := range today.Revenue {
				if r.Currency == c {
					m.MRRMinor, m.Accounts = r.MRRMinor, r.Accounts
				}
			}
			if err := store.SaveMRR(ctx, db, m, now); err != nil {
				return err
			}
		}
		_, err := store.StartMonth(ctx, db, w.ThisMonth(), w.Today, today.Accounts)
		return err
	})
}

// Growth is the console's growth page. A section that could not be read
// is nil and named in Errors.
type Growth struct {
	ComputedAt time.Time
	Months     []time.Time
	Collected  []domain.CurrencyMonths // nil when unread
	Stores     []domain.MonthCount     // nil when unread
	MRR        []store.DayMRR          // nil when unread; from the first reading on
	Churn      []domain.MonthChurn     // nil when unread; complete months only
	Plans      []billing.PlanRevenue   // nil when unread
	Paying     *int64                  // the paying accounts now; nil when unread
	Errors     []string
}

// Growth reads the page's figures, each section under its own timeout.
// Like Metrics, a failure is logged and named, never returned.
func (s *Service) Growth(ctx context.Context) (Growth, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return Growth{}, errNoSession
	}
	loc, err := cairo()
	if err != nil {
		return Growth{}, err
	}
	now := s.now().UTC()
	w := domain.GrowthWindowAt(now, loc)
	g := Growth{ComputedAt: now, Months: w.Months}
	check := func(section string, f func(context.Context) error) {
		cctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		if err := f(cctx); err != nil {
			s.d.Log.WarnContext(ctx, "admin: growth failed", "section", section, "err", err)
			g.Errors = append(g.Errors, section)
		}
	}
	billingOn := func() error {
		if s.d.Billing == nil {
			return errNoBilling
		}
		return nil
	}
	check("collected", func(ctx context.Context) error {
		if err := billingOn(); err != nil {
			return err
		}
		rows, err := s.d.Billing.Collected(ctx, w.Since)
		if err != nil {
			return err
		}
		amounts := make([]domain.MonthAmount, len(rows))
		for i, r := range rows {
			amounts[i] = domain.MonthAmount{Month: r.Month, Currency: r.Currency, Invoices: r.Invoices, AmountMinor: r.SubtotalMinor}
		}
		g.Collected = w.FillCollected(amounts)
		return nil
	})
	check("stores", func(ctx context.Context) error {
		if err := billingOn(); err != nil {
			return err
		}
		converted, err := s.d.Billing.Converted(ctx, w.Since)
		if err != nil {
			return err
		}
		rows, err := s.d.Directory.StoresByMonth(ctx, w.Since, converted)
		if err != nil {
			return err
		}
		counts := make([]domain.MonthCount, len(rows))
		for i, r := range rows {
			counts[i] = domain.MonthCount{Month: r.Month, Created: r.Created, Converted: r.Converted}
		}
		g.Stores = w.FillStores(counts)
		return nil
	})
	check("mrr", func(ctx context.Context) error {
		days, err := store.MRRSince(ctx, s.d.DB, w.Months[0])
		if err == nil {
			g.MRR = days
		}
		return err
	})
	check("churn", func(ctx context.Context) error {
		months, err := store.Churn(ctx, s.d.DB, w.Months[0])
		if err == nil {
			g.Churn = months
		}
		return err
	})
	check("plans", func(ctx context.Context) error {
		if err := billingOn(); err != nil {
			return err
		}
		today, err := s.d.Billing.Today(ctx)
		if err != nil {
			return err
		}
		g.Plans = append([]billing.PlanRevenue{}, today.Plans...)
		n := int64(len(today.Accounts))
		g.Paying = &n
		return nil
	})
	return g, nil
}
