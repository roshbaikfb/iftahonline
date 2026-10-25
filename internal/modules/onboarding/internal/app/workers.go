package app

import (
	"context"

	"github.com/riverqueue/river"
)

// AddWorkers registers the onboarding job workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	return river.AddWorkerSafely(w, &applyWorker{svc: s})
}

type applyWorker struct {
	river.WorkerDefaults[ApplyTemplateArgs]
	svc *Service
}

func (w *applyWorker) Work(ctx context.Context, job *river.Job[ApplyTemplateArgs]) error {
	return w.svc.ApplyTemplate(ctx, job.Args.Seq, job.Attempt >= job.MaxAttempts)
}
