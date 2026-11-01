package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
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
// time; a payment still not paid expires, and its order is cancelled when
// none of its payments is open or captured.
func (s *Service) ExpirePayment(ctx context.Context, id ulid.ID) error {
	p, err := s.payment(ctx, id)
	if errors.Is(err, ErrPaymentNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Status.Open() {
		if err = s.fetchAndApply(ctx, p); err != nil && !errors.Is(err, ErrConnectionNotFound) {
			return err
		}
		if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			cur, txErr := store.GetPayment(ctx, tx, id, true)
			if txErr != nil || !cur.Status.Open() {
				return txErr
			}
			cur.Status, cur.Failure = domain.StatusExpired, "not paid within the hour"
			_, txErr = store.SetState(ctx, tx, cur)
			return txErr
		}); err != nil {
			return err
		}
	}
	// Also when the payment had expired already: a retried job finishes
	// what a failed one began.
	return s.cancelUnpaid(ctx, p.OrderID)
}

// ExpireOrderArgs ends a card order's wait for its payment, ExpireAfter
// after it was placed, whether or not its shopper ever reached the
// gateway. Scheduled by WatchOrder when the order is placed.
type ExpireOrderArgs struct {
	jobs.TenantArgs
	OrderID ulid.ID `json:"order_id"`
}

// Kind implements river.JobArgs.
func (ExpireOrderArgs) Kind() string { return "payments.expire_order" }

// WatchOrder schedules a new card order's expiry; it handles
// orders.order.placed, which is published with the order, so every card
// order gets one. Other orders are ignored.
func (s *Service) WatchOrder(ctx context.Context, orderID ulid.ID) error {
	o, err := s.orders.Get(ctx, orderID)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, due := o.OnlineDue(); !due { // a card order's total, or a prepaid delivery (ADR-0067)
		return nil
	}
	at := o.CreatedAt.Add(ExpireAfter)
	if earliest := s.now().Add(time.Minute); at.Before(earliest) {
		at = earliest
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ExpireOrderArgs{TenantArgs: t, OrderID: orderID} },
			&river.InsertOpts{ScheduledAt: at, UniqueOpts: river.UniqueOpts{ByArgs: true}})
	})
}

// ExpireOrder runs ExpireOrderArgs: its open payments expire (each asked
// about one last time), then the order is cancelled if nothing is open
// or captured.
func (s *Service) ExpireOrder(ctx context.Context, orderID ulid.ID) error {
	ps, err := s.OrderPayments(ctx, orderID)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Status.Open() {
			if err = s.ExpirePayment(ctx, p.ID); err != nil {
				return err
			}
		}
	}
	return s.cancelUnpaid(ctx, orderID)
}

// cancelUnpaid cancels a card order still pending and awaiting its
// payment when none of its payments is open or captured; its stock comes
// back. The order and its payments are locked: a payment the gateway
// confirms meanwhile waits, then finds the order cancelled and is
// refunded (orderFollowsTx), and a merchant's move wins.
func (s *Service) cancelUnpaid(ctx context.Context, orderID ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		o, err := s.orders.GetTx(ctx, tx, orderID)
		if errors.Is(err, orders.ErrOrderNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, due := o.OnlineDue(); o.Status != orders.StatusPending || !due {
			return nil
		}
		ps, err := store.LockOrderPayments(ctx, tx, orderID)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.Status.Open() || p.Status.Captured() {
				return nil
			}
		}
		_, err = s.orders.TransitionSystemTx(ctx, tx, orderID, orders.StatusCancelled, expiryActor, "not paid within the hour")
		if errors.Is(err, orders.ErrInvalidTransition) {
			return nil
		}
		return err
	})
}

// errRefundWaiting: an older refund of the same payment is still to be
// answered; this one runs after it.
var errRefundWaiting = errors.New("payments: an older refund of this payment is not answered yet")

// IsRefundWaiting reports whether a refund job waits for an older refund
// of its payment (the worker snoozes it).
func IsRefundWaiting(err error) bool { return errors.Is(err, errRefundWaiting) }

// refundWait is how long a refund waits for an older one of its payment.
const refundWait = 30 * time.Second

// older reports whether a was asked before b (IDs break ties).
func older(a, b domain.Refund) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return bytes.Compare(a.ID[:], b.ID[:]) < 0
}

// refundTimeout bounds asking the gateway and refunding, while the
// payment's row is held.
const refundTimeout = 20 * time.Second

// RefundPayment runs RefundPaymentArgs: the gateway refunds, the refund is
// recorded as made, and a sync job brings the payment's new amounts. A
// refusal fails the refund with the reason.
//
// The payment's row is held for the whole job, so the refunds of one
// payment run one at a time, oldest first. Paymob takes no idempotency
// key: before refunding, the job asks the gateway what it refunded, and
// when that is more than the refunds recorded as made, the oldest
// unanswered refund (this one) went through already (its answer was
// lost) and is recorded instead of sent again.
func (s *Service) RefundPayment(ctx context.Context, id ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		r, err := store.GetRefund(ctx, tx, id)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil || r.Status != domain.RefundRequested {
			return err
		}
		p, err := store.GetPayment(ctx, tx, r.PaymentID, true)
		if err != nil {
			return err
		}
		all, err := store.Refunds(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		var made int64
		for _, other := range all {
			switch {
			case other.Status == domain.RefundSucceeded:
				made += other.Amount.Amount()
			case other.Status == domain.RefundRequested && other.ID != r.ID && older(other, r):
				return errRefundWaiting
			}
		}
		c, err := store.GetConnection(ctx, tx, p.ConnectionID)
		if err != nil {
			return err
		}
		g, creds, err := s.open(ctx, c, true)
		if err != nil {
			return err
		}
		gctx, cancel := context.WithTimeout(ctx, refundTimeout)
		defer cancel()
		st, err := g.FetchStatus(gctx, creds, p.ID.String(), p.Amount.Currency())
		if err != nil {
			return err
		}
		refundID := ""
		if st.Refunded.Amount() < made+r.Amount.Amount() {
			refundID, err = g.Refund(gctx, creds, p.TransactionID, r.Amount)
			if errors.Is(err, gateway.ErrRejected) || errors.Is(err, gateway.ErrAuth) {
				return store.SetRefund(ctx, tx, id, domain.RefundFailed, "", text.Clip(err.Error(), 300))
			}
			if err != nil {
				return err // maybe made: the retried job asks the gateway first
			}
		}
		if err = store.SetRefund(ctx, tx, id, domain.RefundSucceeded, refundID, ""); err != nil {
			return err
		}
		return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return SyncPaymentArgs{TenantArgs: t, PaymentID: p.ID} }, nil)
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
		river.AddWorkerSafely(w, &expireOrderWorker{svc: s}),
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
	err := w.svc.RefundPayment(ctx, job.Args.RefundID)
	if IsRefundWaiting(err) {
		return river.JobSnooze(refundWait)
	}
	return err
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

type expireOrderWorker struct {
	river.WorkerDefaults[ExpireOrderArgs]
	svc *Service
}

func (w *expireOrderWorker) Work(ctx context.Context, job *river.Job[ExpireOrderArgs]) error {
	return w.svc.ExpireOrder(ctx, job.Args.OrderID)
}
