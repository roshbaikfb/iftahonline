package app

import (
	"context"
	"errors"

	"github.com/riverqueue/river"
)

// AddWorkers registers the shipping job workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	return errors.Join(
		river.AddWorkerSafely(w, &createWorker{svc: s}),
		river.AddWorkerSafely(w, &labelWorker{svc: s}),
		river.AddWorkerSafely(w, &cancelWorker{svc: s}),
		river.AddWorkerSafely(w, &trackWorker{svc: s}),
		river.AddWorkerSafely(w, &webhookWorker{svc: s}),
		river.AddWorkerSafely(w, &pullWorker{svc: s}),
	)
}

type createWorker struct {
	river.WorkerDefaults[CreateShipmentArgs]
	svc *Service
}

func (w *createWorker) Work(ctx context.Context, job *river.Job[CreateShipmentArgs]) error {
	return w.svc.CreateShipment(ctx, job.Args.ShipmentID)
}

type labelWorker struct {
	river.WorkerDefaults[FetchLabelArgs]
	svc *Service
}

func (w *labelWorker) Work(ctx context.Context, job *river.Job[FetchLabelArgs]) error {
	return w.svc.FetchLabel(ctx, job.Args.ShipmentID)
}

type cancelWorker struct {
	river.WorkerDefaults[CancelShipmentArgs]
	svc *Service
}

func (w *cancelWorker) Work(ctx context.Context, job *river.Job[CancelShipmentArgs]) error {
	return w.svc.CancelShipment(ctx, job.Args.ShipmentID)
}

// trackWorker polls one shipment for as long as it is active: after each
// poll it snoozes PollEvery (a snooze uses no attempt), so a carrier
// outage spends attempts only while it lasts.
type trackWorker struct {
	river.WorkerDefaults[TrackShipmentArgs]
	svc *Service
}

func (w *trackWorker) Work(ctx context.Context, job *river.Job[TrackShipmentArgs]) error {
	again, err := w.svc.TrackShipment(ctx, job.Args.ShipmentID)
	if err != nil {
		return err
	}
	if again {
		return river.JobSnooze(PollEvery)
	}
	return nil
}

type webhookWorker struct {
	river.WorkerDefaults[ProcessWebhookArgs]
	svc *Service
}

func (w *webhookWorker) Work(ctx context.Context, job *river.Job[ProcessWebhookArgs]) error {
	return w.svc.ProcessWebhook(ctx, job.Args.ReceiptID)
}

// pullWorker pulls a connection's settlements, then snoozes PullEvery;
// it ends for carriers without a settlements API and for disconnected
// connections.
type pullWorker struct {
	river.WorkerDefaults[PullSettlementsArgs]
	svc *Service
}

func (w *pullWorker) Work(ctx context.Context, job *river.Job[PullSettlementsArgs]) error {
	again, err := w.svc.PullSettlements(ctx, job.Args.ConnectionID)
	if err != nil {
		return err
	}
	if again {
		return river.JobSnooze(PullEvery)
	}
	return nil
}
