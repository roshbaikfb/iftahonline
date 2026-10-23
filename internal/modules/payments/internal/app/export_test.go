package app

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"
)

// RunJob runs an enqueued payment job through its worker, as River would.
func (s *Service) RunJob(ctx context.Context, args river.JobArgs) error {
	switch a := args.(type) {
	case ExpirePaymentArgs:
		return (&expireWorker{svc: s}).Work(ctx, &river.Job[ExpirePaymentArgs]{Args: a})
	case RefundPaymentArgs:
		return (&refundWorker{svc: s}).Work(ctx, &river.Job[RefundPaymentArgs]{Args: a})
	case ProcessWebhookArgs:
		return (&webhookWorker{svc: s}).Work(ctx, &river.Job[ProcessWebhookArgs]{Args: a})
	case SyncPaymentArgs:
		return (&syncWorker{svc: s}).Work(ctx, &river.Job[SyncPaymentArgs]{Args: a})
	}
	return fmt.Errorf("not a payments job: %s", args.Kind())
}
