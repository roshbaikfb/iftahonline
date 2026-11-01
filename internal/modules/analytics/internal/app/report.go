package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// MaxRangeDays bounds a report.
const MaxRangeDays = 366

// Report is the profit dashboard over [From, To): the totals, each day's
// totals, and one dimension's breakdown.
type Report struct {
	From, To time.Time
	Currency string
	Totals   domain.Figures
	Days     []store.DayFigures
	By       domain.Dimension
	Rows     []domain.Row
}

func checkRange(from, to time.Time) error {
	if !from.Before(to) || to.Sub(from) > MaxRangeDays*24*time.Hour {
		return errs.Validation(errs.FieldError{Field: "to", Code: "range", Message: fmt.Sprintf("the range is 1 to %d days", MaxRangeDays)})
	}
	return nil
}

// Profit reads the dashboard over the store-local days [from, to) on the
// read pool only, in one read-only transaction of three queries; the
// plan's profit feature is needed (403 plan_limit).
func (s *Service) Profit(ctx context.Context, from, to time.Time, by domain.Dimension) (Report, error) {
	if s.read == nil {
		return Report{}, errNoRead
	}
	if err := checkRange(from, to); err != nil {
		return Report{}, err
	}
	if err := s.plan.Require(ctx, entitlements.Profit); err != nil {
		return Report{}, err
	}
	info, err := s.stores(ctx)
	if err != nil {
		return Report{}, err
	}
	r := Report{From: from, To: to, Currency: info.Currency, By: by}
	err = postgres.InTenantTx(ctx, s.read, func(tx pgx.Tx) (err error) {
		if r.Totals, err = store.Totals(ctx, tx, from, to); err != nil {
			return err
		}
		if r.Days, err = store.Days(ctx, tx, from, to); err != nil {
			return err
		}
		r.Rows, err = store.Breakdown(ctx, tx, by, from, to)
		return err
	})
	return r, err
}

// Settings returns the store's delivery cost estimates.
func (s *Service) Settings(ctx context.Context) (domain.Settings, error) {
	var out domain.Settings
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.Settings(ctx, tx)
		return err
	})
	return out, err
}

// SaveSettings replaces the store's estimates; every day is computed
// again with them. Audited.
func (s *Service) SaveSettings(ctx context.Context, in domain.Settings) (domain.Settings, error) {
	if err := in.Validate(); err != nil {
		return domain.Settings{}, err
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveSettings(ctx, tx, in); err != nil {
			return err
		}
		if err := store.MarkAllDirty(ctx, tx); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "analytics.settings_updated", TargetType: "profit_settings"})
	})
	if err != nil {
		return domain.Settings{}, err
	}
	return in, nil
}

// Platforms are the ad platforms spend is recorded for.
var platforms = map[string]bool{"meta": true, "tiktok": true, "google": true, "snap": true, "other": true} //nolint:gochecknoglobals // a constant set

// AdSpendInput is what a merchant types in.
type AdSpendInput struct {
	Day         time.Time
	Platform    string
	Campaign    string
	AmountMinor int64
	Note        string
}

// AddAdSpend records a day's spend; its day is computed again. The day is
// at most two years back and not in the future (the store's day).
func (s *Service) AddAdSpend(ctx context.Context, in AdSpendInput) (store.AdSpend, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return store.AdSpend{}, err
	}
	today, err := time.Parse(store.DayLayout, s.now().In(info.Location).Format(store.DayLayout))
	if err != nil {
		return store.AdSpend{}, err
	}
	var fields []errs.FieldError
	if in.Day.After(today) || in.Day.Before(today.AddDate(-2, 0, 0)) {
		fields = append(fields, errs.FieldError{Field: "day", Code: "range", Message: "a day of the last two years"})
	}
	if !platforms[in.Platform] {
		fields = append(fields, errs.FieldError{Field: "platform", Code: "invalid", Message: "meta, tiktok, google, snap or other"})
	}
	if in.AmountMinor < 0 || in.AmountMinor > 1_000_000_000_000 {
		fields = append(fields, errs.FieldError{Field: "amount_minor", Code: "range", Message: "between 0 and 1000000000000"})
	}
	campaign, err := text.Clean(in.Campaign, "campaign", 0, 100)
	if err != nil {
		return store.AdSpend{}, err
	}
	note, err := text.Clean(in.Note, "note", 0, 200)
	if err != nil {
		return store.AdSpend{}, err
	}
	if len(fields) > 0 {
		return store.AdSpend{}, errs.Validation(fields...)
	}
	a := store.AdSpend{ID: s.ids.New(), Day: in.Day, Platform: in.Platform, Campaign: campaign, Currency: info.Currency,
		Amount: in.AmountMinor, Source: "manual", Note: note}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		a, err = store.InsertAdSpend(ctx, tx, a)
		return err
	})
	return a, err
}

// DeleteAdSpend removes an entry of the store's; ErrNotFound otherwise.
func (s *Service) DeleteAdSpend(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.DeleteAdSpend(ctx, tx, id) })
	if err == store.ErrNotFound { //nolint:errorlint // the store returns the sentinel itself
		return ErrNotFound
	}
	return err
}

// AdSpend lists the store's entries of [from, to), newest first.
func (s *Service) AdSpend(ctx context.Context, from, to time.Time) ([]store.AdSpend, error) {
	if err := checkRange(from, to); err != nil {
		return nil, err
	}
	var out []store.AdSpend
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.ListAdSpend(ctx, tx, from, to)
		return err
	})
	return out, err
}

// AdSpendLine is one day's spend of one campaign, as an ad platform's
// importer reports it.
type AdSpendLine struct {
	Day         time.Time
	Campaign    string
	AmountMinor int64
}

// AdSpendSource is an ad platform's spend reporting: the importers that
// come later (Meta, TikTok, Google) implement it (ADR-0069).
type AdSpendSource interface {
	// Platform is one of meta, tiktok, google, snap.
	Platform() string
	// Spend reports the store's spend of [from, to), in the store's
	// currency.
	Spend(ctx context.Context, from, to time.Time) ([]AdSpendLine, error)
}

// ImportAdSpend replaces what src said of [from, to) before with what it
// says now; manual entries stay. The days are computed again.
func (s *Service) ImportAdSpend(ctx context.Context, src AdSpendSource, from, to time.Time) (int, error) {
	if err := checkRange(from, to); err != nil {
		return 0, err
	}
	platform := strings.ToLower(src.Platform())
	if !platforms[platform] || platform == "other" {
		return 0, fmt.Errorf("analytics: unknown ad platform %q", src.Platform())
	}
	info, err := s.stores(ctx)
	if err != nil {
		return 0, err
	}
	lines, err := src.Spend(ctx, from, to)
	if err != nil {
		return 0, fmt.Errorf("analytics: %s spend: %w", platform, err)
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if rErr := store.ReplaceImported(ctx, tx, platform, from, to); rErr != nil {
			return rErr
		}
		for _, l := range lines {
			if l.Day.Before(from) || !l.Day.Before(to) || l.AmountMinor < 0 {
				continue
			}
			if _, iErr := store.InsertAdSpend(ctx, tx, store.AdSpend{ID: s.ids.New(), Day: l.Day, Platform: platform,
				Campaign: text.Clip(strings.TrimSpace(l.Campaign), 100), Currency: info.Currency, Amount: l.AmountMinor, Source: "import"}); iErr != nil {
				return iErr
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(lines), nil
}

// Job args.
type (
	// BackfillArgs keeps one store's past orders.
	BackfillArgs struct{ jobs.TenantArgs }
	// RollupArgs computes one store's dirty days.
	RollupArgs struct{ jobs.TenantArgs }
	// SweepArgs records one store's carrier charges.
	SweepArgs struct{ jobs.TenantArgs }
	// RollupsArgs queues RollupArgs for every store with dirty days.
	RollupsArgs struct{ jobs.GlobalArgs }
	// SweepsArgs queues SweepArgs for every store with uncharged orders.
	SweepsArgs struct{ jobs.GlobalArgs }
)

// Kind implements river.JobArgs.
func (BackfillArgs) Kind() string { return "analytics.backfill" }

// Kind implements river.JobArgs.
func (RollupArgs) Kind() string { return "analytics.rollup" }

// Kind implements river.JobArgs.
func (SweepArgs) Kind() string { return "analytics.sweep" }

// Kind implements river.JobArgs.
func (RollupsArgs) Kind() string { return "analytics.rollups" }

// Kind implements river.JobArgs.
func (SweepsArgs) Kind() string { return "analytics.sweeps" }

// Periodic is the module's schedule: dirty days every 10 minutes, carrier
// charges daily.
func Periodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return RollupsArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return SweepsArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// fanOut queues mk's job for each store list returns, as that store.
func (s *Service) fanOut(ctx context.Context, list func(context.Context, store.Tx) ([]ulid.ID, error), mk func(jobs.TenantArgs) river.JobArgs) error {
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		ids, err := list(ctx, tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			sid := tenant.StoreID{ID: id}
			if _, err = s.jobs.InsertTx(tenant.WithStore(ctx, sid), tx, mk(jobs.TenantArgs{StoreID: sid}), jobs.Unique()); err != nil {
				return fmt.Errorf("analytics: enqueue: %w", err)
			}
		}
		return nil
	})
}

type worker[T river.JobArgs] struct {
	river.WorkerDefaults[T]
	run func(context.Context) error
}

func (w *worker[T]) Work(ctx context.Context, _ *river.Job[T]) error { return w.run(ctx) }

// Timeout gives a big store's backfill or rollup the time it needs.
func (*worker[T]) Timeout(*river.Job[T]) time.Duration { return 15 * time.Minute }

// AddWorkers registers the module's workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	discard := func(f func(context.Context) (int, error)) func(context.Context) error {
		return func(ctx context.Context) error { _, err := f(ctx); return err }
	}
	for _, add := range []func() error{
		func() error { return river.AddWorkerSafely(w, &worker[BackfillArgs]{run: discard(s.Backfill)}) },
		func() error { return river.AddWorkerSafely(w, &worker[RollupArgs]{run: discard(s.Rollup)}) },
		func() error { return river.AddWorkerSafely(w, &worker[SweepArgs]{run: discard(s.Sweep)}) },
		func() error {
			return river.AddWorkerSafely(w, &worker[RollupsArgs]{run: func(ctx context.Context) error {
				return s.fanOut(ctx, store.DirtyStores, func(t jobs.TenantArgs) river.JobArgs { return RollupArgs{TenantArgs: t} })
			}})
		},
		func() error {
			return river.AddWorkerSafely(w, &worker[SweepsArgs]{run: func(ctx context.Context) error {
				return s.fanOut(ctx, store.SweepStores, func(t jobs.TenantArgs) river.JobArgs { return SweepArgs{TenantArgs: t} })
			}})
		},
	} {
		if err := add(); err != nil {
			return fmt.Errorf("analytics: %w", err)
		}
	}
	return nil
}
