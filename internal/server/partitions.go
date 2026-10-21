package server

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/server/dbgen"
)

// PartitionsArgs keeps every monthly partitioned table (audit_log,
// orders, order items, order events) partitioned ahead of time. The
// tables register themselves in app.monthly_partitioned (migration
// 00022), so a module adding one needs no new job.
type PartitionsArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (PartitionsArgs) Kind() string { return "platform.partitions" }

// PartitionsMonthsAhead is how far ahead partitions exist. Three months
// leave a quarter for a failing job to be noticed and fixed.
const PartitionsMonthsAhead = 3

// PartitionsWorker runs PartitionsArgs.
type PartitionsWorker struct {
	river.WorkerDefaults[PartitionsArgs]
	DB dbgen.DBTX
}

// Work implements river.Worker.
func (w *PartitionsWorker) Work(ctx context.Context, _ *river.Job[PartitionsArgs]) error {
	if _, err := dbgen.New(w.DB).EnsureMonthlyPartitions(ctx, PartitionsMonthsAhead); err != nil {
		return fmt.Errorf("partitions: %w", err)
	}
	return nil
}

// PartitionsPeriodic runs the job daily and once at worker start.
func PartitionsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PartitionsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
