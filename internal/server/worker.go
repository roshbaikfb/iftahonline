package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/server/dbgen"
)

// RiverSchemaVersion is the River migration version applied by
// db/migrations (00002..00009). Bump it with the next River migration.
const RiverSchemaVersion = 8

// NoopArgs is a job that does nothing. It keeps the worker's River wiring
// real (River requires at least one worker) and lets smoke tests verify
// the pipeline end to end.
type NoopArgs struct{}

// Kind implements river.JobArgs.
func (NoopArgs) Kind() string { return "platform.noop" }

type noopWorker struct {
	river.WorkerDefaults[NoopArgs]
}

func (noopWorker) Work(context.Context, *river.Job[NoopArgs]) error { return nil }

// NewRiverClient builds the worker's River client. Modules add their
// workers to `workers` before the client is created.
func NewRiverClient(pool *pgxpool.Pool, c config.Worker, log *slog.Logger, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	if err := river.AddWorkerSafely(workers, &noopWorker{}); err != nil {
		return nil, fmt.Errorf("river: add noop worker: %w", err)
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:     log,
		Queues:     map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: c.Concurrency}},
		Workers:    workers,
		JobTimeout: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("river: new client: %w", err)
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
