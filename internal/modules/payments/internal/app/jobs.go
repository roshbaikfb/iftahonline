package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ExpirePaymentArgs ends a card order's wait for a payment: ExpireAfter
// after the order was placed.
type ExpirePaymentArgs struct {
	jobs.TenantArgs
	PaymentID ulid.ID `json:"payment_id"`
}

// Kind implements river.JobArgs.
func (ExpirePaymentArgs) Kind() string { return "payments.expire_payment" }

// RefundPaymentArgs asks the gateway for a requested refund.
type RefundPaymentArgs struct {
	jobs.TenantArgs
	RefundID ulid.ID `json:"refund_id"`
}

// Kind implements river.JobArgs.
func (RefundPaymentArgs) Kind() string { return "payments.refund_payment" }

// ProcessWebhookArgs processes an accepted gateway webhook.
type ProcessWebhookArgs struct {
	jobs.TenantArgs
	ReceiptID ulid.ID `json:"receipt_id"`
}

// Kind implements river.JobArgs.
func (ProcessWebhookArgs) Kind() string { return "payments.process_webhook" }

// ExpirePayment runs ExpirePaymentArgs: it asks the gateway one last
// time; a payment still not paid expires, and when none of its order's
// payments is open or captured, the order is cancelled (its stock comes
// back).
func (s *Service) ExpirePayment(ctx context.Context, id ulid.ID) error {
	p, err := s.payment(ctx, id)
	if errors.Is(err, ErrPaymentNotFound) || (err == nil && !p.Status.Open()) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.fetchAndApply(ctx, p); err != nil && !errors.Is(err, ErrConnectionNotFound) {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, txErr := store.GetPayment(ctx, tx, id, true)
		if txErr != nil || !cur.Status.Open() {
			return txErr
		}
		cur.Status, cur.Failure = domain.StatusExpired, "not paid within the hour"
		if _, txErr = store.SetState(ctx, tx, cur); txErr != nil {
			return txErr
		}
		all, txErr := store.OrderPayments(ctx, tx, cur.OrderID)
		if txErr != nil {
			return txErr
		}
		for _, q := range all {
			if q.ID != cur.ID && (q.Status.Open() || q.Status.Captured()) {
				return nil // another attempt is still open or paid
			}
		}
		o, txErr := s.orders.Get(ctx, cur.OrderID)
		if txErr != nil || o.Status != orders.StatusPending || o.PaymentStatus != orders.PaymentAwaiting {
			return txErr
		}
		_, txErr = s.orders.TransitionSystemTx(ctx, tx, cur.OrderID, orders.StatusCancelled, expiryActor, "not paid within the hour")
		if errors.Is(txErr, orders.ErrInvalidTransition) {
			return nil
		}
		return txErr
	})
}

// RefundPayment runs RefundPaymentArgs: the gateway refunds, the refund is
// recorded as made, and a sync job brings the payment's new amounts. A
// refusal fails the refund with the reason.
func (s *Service) RefundPayment(ctx context.Context, id ulid.ID) error {
	var r domain.Refund
	var p domain.Payment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if r, txErr = store.GetRefund(ctx, tx, id); txErr != nil {
			return txErr
		}
		p, txErr = store.GetPayment(ctx, tx, r.PaymentID, false)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) || (err == nil && r.Status != domain.RefundRequested) {
		return nil
	}
	if err != nil {
		return err
	}
	c, err := s.connection(ctx, p.ConnectionID)
	if err != nil {
		return err
	}
	g, creds, err := s.open(ctx, c, true)
	if err != nil {
		return err
	}
	// Paymob takes no idempotency key for refunds: if a run died after
	// the gateway refunded and before that was recorded, the gateway
	// already shows more refunded than this payment knows of, and the
	// refund is recorded instead of asked for again.
	st, err := g.FetchStatus(ctx, creds, p.ID.String(), p.Amount.Currency())
	if err != nil {
		return err
	}
	if st.Refunded.Amount() >= p.Refunded.Amount()+r.Amount.Amount() {
		return s.recordRefundTx(ctx, id, p.ID, "")
	}
	refundID, err := g.Refund(ctx, creds, p.TransactionID, r.Amount)
	if errors.Is(err, gateway.ErrRejected) || errors.Is(err, gateway.ErrAuth) {
		return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			return store.SetRefund(ctx, tx, id, domain.RefundFailed, "", clip(err.Error()))
		})
	}
	if err != nil {
		return err
	}
	// The gateway refunded: that is recorded before anything else can
	// fail, so a retry of this job never asks for the refund twice.
	return s.recordRefundTx(ctx, id, p.ID, refundID)
}

// recordRefundTx marks a refund made; the payment's new amounts come
// from the gateway in a job of their own.
func (s *Service) recordRefundTx(ctx context.Context, refundID, paymentID ulid.ID, providerID string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := store.SetRefund(ctx, tx, refundID, domain.RefundSucceeded, providerID, ""); txErr != nil {
			return txErr
		}
		return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return SyncPaymentArgs{TenantArgs: t, PaymentID: paymentID} }, nil)
	})
}

// SyncPaymentArgs fetches a payment's status from the gateway and
// applies it (after a refund).
type SyncPaymentArgs struct {
	jobs.TenantArgs
	PaymentID ulid.ID `json:"payment_id"`
}

// Kind implements river.JobArgs.
func (SyncPaymentArgs) Kind() string { return "payments.sync_payment" }

// SyncPayment runs SyncPaymentArgs.
func (s *Service) SyncPayment(ctx context.Context, id ulid.ID) error {
	p, err := s.payment(ctx, id)
	if errors.Is(err, ErrPaymentNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.fetchAndApply(ctx, p)
}

// ProcessWebhook runs ProcessWebhookArgs: the receipt's body names a
// payment, whose status is fetched from the gateway before anything
// changes (ADR-0024); the receipt's outcome is recorded with the change.
func (s *Service) ProcessWebhook(ctx context.Context, receiptID ulid.ID) error {
	r, _, body, err := s.inbox.Get(ctx, receiptID)
	if errors.Is(err, webhooks.ErrReceiptNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	g, ok := s.gateways[r.Provider]
	if !ok {
		return river.JobCancel(fmt.Errorf("payments: no gateway %q", r.Provider))
	}
	ev, err := g.ParseWebhook(body)
	if err != nil {
		return s.markReceipt(ctx, receiptID, "unreadable")
	}
	pid, err := ulid.Parse(ev.Reference)
	if err != nil {
		return s.markReceipt(ctx, receiptID, "unknown_payment")
	}
	p, err := s.payment(ctx, pid)
	if errors.Is(err, ErrPaymentNotFound) {
		return s.markReceipt(ctx, receiptID, "unknown_payment")
	}
	if err != nil {
		return err
	}
	c, err := s.connection(ctx, p.ConnectionID)
	if err != nil {
		return err
	}
	gw, creds, err := s.open(ctx, c, true)
	if err != nil {
		return err
	}
	st, err := gw.FetchStatus(ctx, creds, p.ID.String(), p.Amount.Currency())
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		before, txErr := store.GetPayment(ctx, tx, p.ID, true)
		if txErr != nil {
			return txErr
		}
		if txErr = s.applyTx(ctx, tx, p.ID, st); txErr != nil {
			return txErr
		}
		after, txErr := store.GetPayment(ctx, tx, p.ID, false)
		if txErr != nil {
			return txErr
		}
		outcome := "unchanged"
		if after.Version != before.Version {
			outcome = "updated"
		}
		_, txErr = s.inbox.MarkProcessedTx(ctx, tx, receiptID, outcome)
		return txErr
	})
}

func (s *Service) markReceipt(ctx context.Context, id ulid.ID, outcome string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := s.inbox.MarkProcessedTx(ctx, tx, id, outcome)
		return err
	})
}

// WebhookProvider is a gateway's side of the webhooks inbox.
type WebhookProvider struct {
	svc     *Service
	gateway gateway.Gateway
}

var _ webhooks.Provider = WebhookProvider{}

// WebhookProviders returns one provider per gateway.
func (s *Service) WebhookProviders() []webhooks.Provider {
	out := make([]webhooks.Provider, 0, len(s.gateways))
	for _, name := range s.gateways.Names() {
		out = append(out, WebhookProvider{svc: s, gateway: s.gateways[name]})
	}
	return out
}

// Name implements webhooks.Provider.
func (p WebhookProvider) Name() string { return p.gateway.Name() }

// Verify implements webhooks.Provider with the connection's gateway
// signature (Paymob: HMAC-SHA512 in ?hmac=).
func (p WebhookProvider) Verify(ctx context.Context, conn ulid.ID, req webhooks.Request) (webhooks.Verified, error) {
	c, err := p.svc.connection(ctx, conn)
	if errors.Is(err, ErrConnectionNotFound) || (err == nil && (c.Gateway != p.gateway.Name() || c.Status != domain.ConnectionActive)) {
		return webhooks.Verified{}, webhooks.ErrUnknownConnection
	}
	if err != nil {
		return webhooks.Verified{}, err
	}
	_, creds, err := p.svc.open(ctx, c, false)
	if err != nil {
		return webhooks.Verified{}, err
	}
	ev, err := p.gateway.VerifyWebhook(creds, gateway.WebhookRequest{Header: req.Header, Query: req.Query, Body: req.Body})
	switch {
	case errors.Is(err, gateway.ErrForged):
		return webhooks.Verified{}, webhooks.ErrForged
	case errors.Is(err, gateway.ErrBadWebhook):
		return webhooks.Verified{}, nil // no event id: the inbox refuses it (400)
	case err != nil:
		return webhooks.Verified{}, err
	}
	return webhooks.Verified{EventID: ev.EventID}, nil
}

// AcceptTx implements webhooks.Provider.
func (p WebhookProvider) AcceptTx(ctx context.Context, tx pgx.Tx, r webhooks.Receipt) error {
	return p.svc.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ProcessWebhookArgs{TenantArgs: t, ReceiptID: r.ID} }, nil)
}

// AddWorkers registers the payment jobs' workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	return errors.Join(
		river.AddWorkerSafely(w, &expireWorker{svc: s}),
		river.AddWorkerSafely(w, &refundWorker{svc: s}),
		river.AddWorkerSafely(w, &webhookWorker{svc: s}),
		river.AddWorkerSafely(w, &syncWorker{svc: s}),
	)
}

type expireWorker struct {
	river.WorkerDefaults[ExpirePaymentArgs]
	svc *Service
}

func (w *expireWorker) Work(ctx context.Context, job *river.Job[ExpirePaymentArgs]) error {
	return w.svc.ExpirePayment(ctx, job.Args.PaymentID)
}

type refundWorker struct {
	river.WorkerDefaults[RefundPaymentArgs]
	svc *Service
}

func (w *refundWorker) Work(ctx context.Context, job *river.Job[RefundPaymentArgs]) error {
	return w.svc.RefundPayment(ctx, job.Args.RefundID)
}

type webhookWorker struct {
	river.WorkerDefaults[ProcessWebhookArgs]
	svc *Service
}

func (w *webhookWorker) Work(ctx context.Context, job *river.Job[ProcessWebhookArgs]) error {
	return w.svc.ProcessWebhook(ctx, job.Args.ReceiptID)
}

type syncWorker struct {
	river.WorkerDefaults[SyncPaymentArgs]
	svc *Service
}

func (w *syncWorker) Work(ctx context.Context, job *river.Job[SyncPaymentArgs]) error {
	return w.svc.SyncPayment(ctx, job.Args.PaymentID)
}
