// Package app holds the risk module's use cases (ADR-0065): learning what
// each order's end teaches about its customer's phone, and scoring
// phones for merchants as a band beside their own history.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// MaxBands is the most orders one Bands call scores.
const MaxBands = store.MaxKeys

// backfillPage is how many past orders the backfill learns per
// transaction.
const backfillPage = 100

// ErrOrderNotFound is an order the store does not have.
var ErrOrderNotFound = errs.NotFound("order_not_found", "order not found")

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	Orders orders.Outcomes
	Keyer  domain.Keyer
	// Jobs queues the store's backfill; nil in roles that never learn
	// (the storefront).
	Jobs jobs.Inserter
	Now  func() time.Time
}

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	orders orders.Outcomes
	keyer  domain.Keyer
	jobs   jobs.Inserter
	now    func() time.Time
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: d.DB, orders: d.Orders, keyer: d.Keyer, jobs: d.Jobs, now: now}
}

func (s *Service) row(o orders.Order) store.Order {
	r := store.Order{ID: o.ID, PhoneKey: s.keyer.Key(o.Contact.Phone), PlacedAt: o.CreatedAt}
	if out, ok := domain.OutcomeOf(string(o.Status), string(o.OutcomeReason)); ok {
		r.Outcome, r.OutcomeAt = out, o.StatusChangedAt
	}
	return r
}

// Learn records what the store's order teaches, as it is now: the
// orders module's events call it, in any order and any number of times.
// The store's first order also queues the backfill of its past. An order
// that does not exist is ErrOrderNotFound.
func (s *Service) Learn(ctx context.Context, id ulid.ID) error {
	o, err := s.orders.Get(ctx, id)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return ErrOrderNotFound
	}
	if err != nil {
		return fmt.Errorf("risk: read order: %w", err)
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.Upsert(ctx, tx, []store.Order{s.row(o)}); err != nil {
			return err
		}
		if s.jobs == nil {
			return nil
		}
		queued, err := store.QueueBackfill(ctx, tx)
		if err != nil || !queued {
			return err
		}
		t, err := jobs.ForTenant(ctx)
		if err != nil {
			return err
		}
		if _, err = s.jobs.InsertTx(ctx, tx, BackfillArgs{TenantArgs: t}, nil); err != nil {
			return fmt.Errorf("risk: queue backfill: %w", err)
		}
		return nil
	})
}

// Backfill learns from all the store's past orders, newest first, a page
// per transaction. It is idempotent: a retry writes nothing new.
func (s *Service) Backfill(ctx context.Context) (int, error) {
	var before ulid.ID
	n := 0
	for {
		page, err := s.orders.Before(ctx, before, backfillPage)
		if err != nil {
			return n, fmt.Errorf("risk: backfill: %w", err)
		}
		rows := make([]store.Order, len(page))
		for i, o := range page {
			rows[i] = s.row(o)
		}
		if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.Upsert(ctx, tx, rows) }); err != nil {
			return n, err
		}
		n += len(page)
		if len(page) < backfillPage {
			return n, nil
		}
		before = page[len(page)-1].ID
	}
}

// Bands scores the customers of some of the store's orders (at most
// MaxBands): each learnt order's band. Orders not learnt yet, or another
// store's, are missing from the result. One transaction, three queries.
func (s *Service) Bands(ctx context.Context, ids []ulid.ID) (map[ulid.ID]domain.Band, error) {
	if len(ids) == 0 || len(ids) > MaxBands {
		return nil, errs.Validation(errs.FieldError{Field: "order_ids", Code: "count", Message: fmt.Sprintf("send 1 to %d orders", MaxBands)})
	}
	out := make(map[ulid.ID]domain.Band, len(ids))
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		keys, err := store.Keys(ctx, tx, ids)
		if err != nil || len(keys) == 0 {
			return err
		}
		distinct := make([][]byte, 0, len(keys))
		seen := make(map[string]bool, len(keys))
		for _, k := range keys {
			if !seen[string(k)] {
				seen[string(k)] = true
				distinct = append(distinct, k)
			}
		}
		signals, err := store.Signals(ctx, tx, distinct)
		if err != nil {
			return err
		}
		now := s.now()
		bands := make(map[string]domain.Band, len(distinct))
		for _, k := range distinct {
			bands[string(k)] = domain.Assess(signals[string(k)], now).Band
		}
		for id, k := range keys {
			out[id] = bands[string(k)]
		}
		return nil
	})
	return out, err
}

// OrderRisk is an order's customer as the merchant sees them: the band,
// and the phone's orders in this store.
type OrderRisk struct {
	Band    domain.Band
	History domain.History
}

// ForOrder scores the customer of one of the store's orders, learning the
// order first if it was not learnt yet. Another store's order is
// ErrOrderNotFound.
func (s *Service) ForOrder(ctx context.Context, id ulid.ID) (OrderRisk, error) {
	r, found, err := s.forOrder(ctx, id)
	if err != nil || found {
		return r, err
	}
	if err = s.Learn(ctx, id); err != nil {
		return OrderRisk{}, err
	}
	if r, found, err = s.forOrder(ctx, id); err == nil && !found {
		err = ErrOrderNotFound
	}
	return r, err
}

func (s *Service) forOrder(ctx context.Context, id ulid.ID) (r OrderRisk, found bool, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		keys, kErr := store.Keys(ctx, tx, []ulid.ID{id})
		if kErr != nil {
			return kErr
		}
		key, ok := keys[id]
		if !ok {
			return nil
		}
		found = true
		signals, sErr := store.Signals(ctx, tx, [][]byte{key})
		if sErr != nil {
			return sErr
		}
		r.Band = domain.Assess(signals[string(key)], s.now()).Band
		var hErr error
		r.History, hErr = store.History(ctx, tx, key)
		return hErr
	})
	return r, found, err
}

// Purge deletes the outcomes past the score's horizon, of every store, in
// batches; it returns how many.
func (s *Service) Purge(ctx context.Context) (int, error) {
	const batch = 5000
	total := 0
	for {
		var n int
		err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (err error) {
			n, err = store.Purge(ctx, tx, batch)
			return err
		})
		total += n
		if err != nil || n < batch {
			return total, err
		}
	}
}

// BackfillArgs learns from one store's past orders.
type BackfillArgs struct {
	jobs.TenantArgs
}

// Kind implements river.JobArgs.
func (BackfillArgs) Kind() string { return "risk.backfill" }

// PurgeArgs deletes outcomes past the horizon, of every store.
type PurgeArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (PurgeArgs) Kind() string { return "risk.purge" }

// PurgePeriodic runs the purge daily.
func PurgePeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PurgeArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

type backfillWorker struct {
	river.WorkerDefaults[BackfillArgs]
	svc *Service
}

// Timeout gives a big store's past the time it needs: about 10 ms a page
// of 100 orders.
func (*backfillWorker) Timeout(*river.Job[BackfillArgs]) time.Duration { return 15 * time.Minute }

func (w *backfillWorker) Work(ctx context.Context, _ *river.Job[BackfillArgs]) error {
	_, err := w.svc.Backfill(ctx)
	return err
}

type purgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	svc *Service
}

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[PurgeArgs]) error {
	_, err := w.svc.Purge(ctx)
	return err
}

// AddWorkers registers the module's job workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	if err := river.AddWorkerSafely(w, &backfillWorker{svc: s}); err != nil {
		return fmt.Errorf("risk: %w", err)
	}
	if err := river.AddWorkerSafely(w, &purgeWorker{svc: s}); err != nil {
		return fmt.Errorf("risk: %w", err)
	}
	return nil
}
