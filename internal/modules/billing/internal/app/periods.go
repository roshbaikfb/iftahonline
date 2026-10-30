package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// EndPeriodsArgs moves every store whose trial or cancelled period ended
// to the free plan.
type EndPeriodsArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (EndPeriodsArgs) Kind() string { return "billing.end_periods" }

// EndPeriodArgs moves one store to the free plan when its period ended.
type EndPeriodArgs struct {
	jobs.TenantArgs
}

// Kind implements river.JobArgs.
func (EndPeriodArgs) Kind() string { return "billing.end_period" }

// saveTx writes a lifecycle step of the store in ctx and publishes
// SubscriptionChanged with it, so caches follow (ADR-0050).
func (s *Service) saveTx(ctx context.Context, tx pgx.Tx, sub domain.Subscription) error {
	if err := store.SaveSubscription(ctx, tx, sub); err != nil {
		return err
	}
	meta, err := event.MetaFor(ctx, s.ids, domain.EventSubscriptionChanged, 1)
	if err != nil {
		return err
	}
	if err = s.events.PublishTx(ctx, tx, domain.SubscriptionChanged{Meta: meta, Status: sub.Status}); err != nil {
		return fmt.Errorf("billing: publish: %w", err)
	}
	return nil
}

// EndPeriods asks to end the period of every store whose trial or
// cancelled period ended by now.
func (s *Service) EndPeriods(ctx context.Context) error {
	if s.jobs == nil {
		return errors.New("billing: no job inserter")
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		ids, err := store.PeriodsEnded(ctx, tx, s.now())
		if err != nil {
			return err
		}
		for _, id := range ids {
			sid := tenant.StoreID{ID: id}
			if _, err = s.jobs.InsertTx(tenant.WithStore(ctx, sid), tx, EndPeriodArgs{TenantArgs: jobs.TenantArgs{StoreID: sid}}, unique()); err != nil {
				return fmt.Errorf("billing: enqueue end period: %w", err)
			}
		}
		return nil
	})
}

// EndPeriod moves the store in ctx to the free plan if its trial or its
// cancelled period has ended; otherwise (paid, renewed meanwhile) it
// does nothing.
func (s *Service) EndPeriod(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		sub, ok, err := store.Subscription(ctx, tx)
		if err != nil || !ok {
			return err
		}
		free, err := store.FreePlan(ctx, tx)
		if err != nil {
			return err
		}
		next, err := sub.EndPeriod(s.now(), free)
		if errors.Is(err, domain.ErrTransition) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.saveTx(ctx, tx, next)
	})
}

// EndPeriodsWorker runs EndPeriodsArgs.
type EndPeriodsWorker struct {
	river.WorkerDefaults[EndPeriodsArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *EndPeriodsWorker) Work(ctx context.Context, _ *river.Job[EndPeriodsArgs]) error {
	return w.Svc.EndPeriods(ctx)
}

// EndPeriodWorker runs EndPeriodArgs.
type EndPeriodWorker struct {
	river.WorkerDefaults[EndPeriodArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *EndPeriodWorker) Work(ctx context.Context, _ *river.Job[EndPeriodArgs]) error {
	return w.Svc.EndPeriod(ctx)
}

// EndPeriodsPeriodic ends periods hourly, and at worker start: a trial
// stops within the hour after it ends.
func EndPeriodsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return EndPeriodsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
