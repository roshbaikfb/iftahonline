package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/server/dbgen"
)

// RiverSchemaVersion is the River migration version applied by
// db/migrations (00002..00009). Bump it with the next River migration.
const RiverSchemaVersion = 8

// NoopArgs is a job that does nothing. It keeps the worker's River wiring
// real (River requires at least one worker) and lets smoke tests verify
// the pipeline end to end.
type NoopArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (NoopArgs) Kind() string { return "platform.noop" }

type noopWorker struct {
	river.WorkerDefaults[NoopArgs]
}

func (noopWorker) Work(context.Context, *river.Job[NoopArgs]) error { return nil }

// NewRiverClient builds the worker's River client. Modules add their
// workers to `workers` before the client is created, and may need queues
// of their own (e.g. a small "media" queue that bounds memory) besides
// the default one. The platform's own jobs (partition upkeep) are added
// here. jobs.Middleware
// rejects unscoped or cross-tenant inserts and restores each job's tenant
// before it runs (ADR-0007).
func NewRiverClient(pool *pgxpool.Pool, c config.Worker, log *slog.Logger, workers *river.Workers, queues map[string]river.QueueConfig, periodic ...*river.PeriodicJob) (*river.Client[pgx.Tx], error) {
	if err := river.AddWorkerSafely(workers, &noopWorker{}); err != nil {
		return nil, fmt.Errorf("river: add noop worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, &PartitionsWorker{DB: pool}); err != nil {
		return nil, fmt.Errorf("river: add partitions worker: %w", err)
	}
	periodic = append(slices.Clone(periodic), PartitionsPeriodic())
	all := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: c.Concurrency}}
	for name, q := range queues {
		if name == river.QueueDefault {
			return nil, fmt.Errorf("river: queue %q is reserved", name)
		}
		all[name] = q
	}
	client, err := river.NewClient(riverpgxv5.New(pool), workerConfig(log, all, workers, periodic))
	if err != nil {
		return nil, fmt.Errorf("river: new client: %w", err)
	}
	return client, nil
}

// FailedJobRetention is how long River keeps a job that was cancelled or
// gave up (P6 review). A job's arguments may carry a shopper's ad signals
// (IP address, user agent, click IDs: ADR-0043), so they are kept long
// enough to look into a failure, and no longer. River keeps a finished
// job a day.
const FailedJobRetention = 72 * time.Hour

// workerConfig is the worker client's River configuration.
func workerConfig(log *slog.Logger, queues map[string]river.QueueConfig, workers *river.Workers, periodic []*river.PeriodicJob) *river.Config {
	return &river.Config{
		Logger:                      log,
		Queues:                      queues,
		Workers:                     workers,
		Middleware:                  jobs.Middleware(),
		PeriodicJobs:                periodic,
		JobTimeout:                  time.Minute,
		CancelledJobRetentionPeriod: FailedJobRetention,
		DiscardedJobRetentionPeriod: FailedJobRetention,
	}
}

// NewRiverInserter builds an insert-only River client for the api and
// storefront roles: modules enqueue with InsertTx in their own
// transactions, and the worker role runs the jobs. It carries the same
// tenant middleware as the worker's client.
func NewRiverInserter(pool *pgxpool.Pool, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:     log,
		Middleware: jobs.Middleware(),
	})
	if err != nil {
		return nil, fmt.Errorf("river: new inserter: %w", err)
	}
	return client, nil
}

// RiverSchemaCheck fails readiness until River's tables are migrated to
// the version this binary expects.
func RiverSchemaCheck(pool *pgxpool.Pool) Check {
	q := dbgen.New(pool)
	return Check{Name: "river_schema", Fn: func(ctx context.Context) error {
		v, err := q.RiverSchemaVersion(ctx)
		if err != nil {
			return fmt.Errorf("river schema: %w", err)
		}
		if v < RiverSchemaVersion {
			return fmt.Errorf("river schema at version %d, need %d", v, RiverSchemaVersion)
		}
		return nil
	}}
}
