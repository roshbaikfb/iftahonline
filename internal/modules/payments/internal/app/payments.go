package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Timing.
const (
	// ExpireAfter is how long a card order waits for its payment before
	// it is cancelled and its stock released (decided with the plan).
	ExpireAfter = 60 * time.Minute
	// ReuseCheckoutFor is how long a pending payment's page is offered
	// again to a shopper who comes back, instead of a new payment.
	ReuseCheckoutFor = 30 * time.Minute
	// createTimeout bounds creating the hosted page while the shopper
	// waits.
	createTimeout = 8 * time.Second
	// DefaultPaymentPage and MaxPaymentPage bound the list.
	DefaultPaymentPage = 50
	MaxPaymentPage     = 200
)

// Actors on orders' timelines and audit logs.
const expiryActor = "system:payment_expiry"

func gatewayActor(gw string) string { return "gateway:" + gw }

var (
	errGatewayDown   = errors.New("payments: the gateway did not create the payment")
	errPageSize      = errs.Validation(errs.FieldError{Field: "limit", Code: "range", Message: "limit must be 1 to 200"})
	errRefundTooMuch = errs.Validation(errs.FieldError{Field: "amount_minor", Code: "range", Message: "more than what is left to refund"})
)

// Checkout is where to send the shopper.
type Checkout struct {
	PaymentID ulid.ID
	URL       string
}

// StartCheckout sends a card order's shopper to the gateway: a pending
// payment (and its expiry job) is written first, then the gateway creates
// the hosted page, bounded by createTimeout while the shopper waits. A
// shopper coming back within ReuseCheckoutFor gets the same page.
func (s *Service) StartCheckout(ctx context.Context, orderID ulid.ID, redirectURL string) (Checkout, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return Checkout{}, err
	}
	o, err := s.orders.Get(ctx, orderID)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return Checkout{}, ErrNotPayable
	}
	if err != nil {
		return Checkout{}, err
	}
	if o.PaymentMethod != orders.PaymentCard || o.PaymentStatus != orders.PaymentAwaiting || o.Status != orders.StatusPending {
		return Checkout{}, ErrNotPayable
	}
	// A plan without card payments (a lapsed trial, a downgrade) sends no
	// shopper to the gateway, connected or not.
	if ok, pErr := s.plan.Allowed(ctx, entitlements.CardPayments); pErr != nil || !ok {
		if pErr != nil {
			return Checkout{}, pErr
		}
		return Checkout{}, ErrCardUnavailable
	}
	var conn store.SealedConnection
	var reuse *domain.Payment
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, txErr := store.ActiveConnection(ctx, tx)
		if txErr != nil {
			return txErr
		}
		conn = c
		ps, txErr := store.OrderPayments(ctx, tx, orderID)
		if txErr != nil {
			return txErr
		}
		for i, p := range ps {
			if p.Status.Open() && p.CheckoutURL != "" && p.ConnectionID == c.ID && s.now().Sub(p.CreatedAt) < ReuseCheckoutFor {
				reuse = &ps[i]
				break
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Checkout{}, ErrCardUnavailable
	case err != nil:
		return Checkout{}, err
	case reuse != nil:
		return Checkout{PaymentID: reuse.ID, URL: reuse.CheckoutURL}, nil
	}
	g, creds, err := s.open(ctx, conn, false)
	if err != nil {
		return Checkout{}, err
	}

	p := domain.Payment{ID: s.ids.New(), OrderID: orderID, ConnectionID: conn.ID, Gateway: conn.Gateway, Amount: o.Total}
	expireAt := o.CreatedAt.Add(ExpireAfter)
	if earliest := s.now().Add(time.Minute); expireAt.Before(earliest) {
		expireAt = earliest
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if p, txErr = store.InsertPayment(ctx, tx, p); txErr != nil {
			return txErr
		}
		return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ExpirePaymentArgs{TenantArgs: t, PaymentID: p.ID} },
			&river.InsertOpts{ScheduledAt: expireAt})
	})
	if err != nil {
		return Checkout{}, err
	}
	first, last := splitName(o.Contact.Name)
	cctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	c, err := g.CreatePayment(cctx, creds, gateway.PaymentRequest{
		Reference: p.ID.String(), Amount: p.Amount, Description: "Order " + o.Code(),
		Customer:        gateway.Customer{FirstName: first, LastName: last, Phone: o.Contact.Phone.Unmasked()},
		NotificationURL: s.WebhookURL(storeID, conn.Connection), RedirectURL: redirectURL,
	})
	if err != nil {
		if failErr := s.fail(ctx, p.ID, text.Clip(err.Error(), 300)); failErr != nil {
			return Checkout{}, failErr
		}
		return Checkout{}, errs.Unavailable(10*time.Second, fmt.Errorf("%w: %w", errGatewayDown, err))
	}
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.SetCheckout(ctx, tx, p.ID, c.URL, c.ProviderRef) }); err != nil {
		return Checkout{}, err
	}
	return Checkout{PaymentID: p.ID, URL: c.URL}, nil
}

func (s *Service) fail(ctx context.Context, id ulid.ID, why string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		p, err := store.GetPayment(ctx, tx, id, true)
		if err != nil || !p.Status.Open() {
			return err
		}
		p.Status, p.Failure = domain.StatusFailed, why
		_, err = store.SetState(ctx, tx, p)
		return err
	})
}

// Refresh asks the gateway about a payment now (the shopper is back from
// the hosted page) and applies its answer.
func (s *Service) Refresh(ctx context.Context, id ulid.ID) (domain.Payment, error) {
	p, err := s.payment(ctx, id)
	if err != nil {
		return domain.Payment{}, err
	}
	if !p.Status.Open() {
		return p, nil
	}
	if err = s.fetchAndApply(ctx, p); err != nil {
		return domain.Payment{}, err
	}
	return s.payment(ctx, id)
}

// RefreshOrder asks the gateway about an order's open payments now (the
// shopper is back from the hosted page), bounded by createTimeout while
// they wait. A gateway that does not answer in time changes nothing: the
// webhook, or the expiry's last inquiry, will.
func (s *Service) RefreshOrder(ctx context.Context, orderID ulid.ID) error {
	ps, err := s.OrderPayments(ctx, orderID)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	var errList []error
	for _, p := range ps {
		if p.Status.Open() {
			errList = append(errList, s.fetchAndApply(rctx, p))
		}
	}
	return errors.Join(errList...)
}

// fetchAndApply asks the gateway about p and applies the answer.
func (s *Service) fetchAndApply(ctx context.Context, p domain.Payment) error {
	c, err := s.connection(ctx, p.ConnectionID)
	if err != nil {
		return err
	}
	g, creds, err := s.open(ctx, c, true)
	if err != nil {
		return err
	}
	st, err := g.FetchStatus(ctx, creds, p.ID.String(), p.Amount.Currency())
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return s.applyTx(ctx, tx, p.ID, st) })
}

// applyTx records what the gateway says of a payment, in tx, and what it
// means for its order: the one place payments change after creation
// (webhooks, expiry, refunds and the shopper's return all land here).
//   - A newly captured payment marks its order paid and confirms a
//     pending one. If the order was already paid by another payment, or
//     was cancelled meanwhile, the money is refunded at once instead.
//   - Refunds mark the order partially refunded or refunded.
func (s *Service) applyTx(ctx context.Context, tx pgx.Tx, id ulid.ID, st gateway.Status) error {
	p, err := store.GetPayment(ctx, tx, id, true)
	if err != nil {
		return err
	}
	w := domain.GatewayWord{Paid: st.State == gateway.StatePaid, Failed: st.State == gateway.StateFailed, Amount: st.Amount,
		Refunded: st.Refunded, TransactionID: st.TransactionID}
	next, refunded, txn, failure := domain.Next(p, w)
	if next == p.Status && refunded.Amount() == p.Refunded.Amount() && txn == p.TransactionID && failure == p.Failure {
		return nil
	}
	was := p
	p.Status, p.Refunded, p.TransactionID, p.Failure = next, refunded, txn, failure
	if p, err = store.SetState(ctx, tx, p); err != nil {
		return err
	}
	if err = s.orderFollowsTx(ctx, tx, was, p); err != nil {
		return err
	}
	if was.Status == p.Status {
		return nil
	}
	m, err := event.MetaFor(ctx, s.ids, domain.EventPaymentStatusChanged, 1)
	if err != nil {
		return err
	}
	return s.events.PublishTx(ctx, tx, domain.PaymentStatusChanged{Meta: m, PaymentID: p.ID, OrderID: p.OrderID, Status: p.Status})
}

func (s *Service) orderFollowsTx(ctx context.Context, tx pgx.Tx, was, p domain.Payment) error {
	if !p.Status.Captured() {
		return nil
	}
	o, err := s.orders.GetTx(ctx, tx, p.OrderID)
	if err != nil {
		return err
	}
	actor := gatewayActor(p.Gateway)
	if !was.Status.Captured() {
		others, listErr := store.OrderPayments(ctx, tx, p.OrderID)
		if listErr != nil {
			return listErr
		}
		for _, q := range others {
			if q.ID != p.ID && q.Status.Captured() {
				return s.autoRefundTx(ctx, tx, p, "the order was already paid by another payment")
			}
		}
		if o.Status == orders.StatusCancelled {
			return s.autoRefundTx(ctx, tx, p, "the order was cancelled before the payment arrived")
		}
		if _, err = s.orders.SetPaymentStatusTx(ctx, tx, p.OrderID, orders.PaymentPaid, actor); err != nil {
			return err
		}
		if o.Status == orders.StatusPending {
			if _, err = s.orders.TransitionSystemTx(ctx, tx, p.OrderID, orders.StatusConfirmed, actor, "paid online"); err != nil &&
				!errors.Is(err, orders.ErrInvalidTransition) {
				return err
			}
		}
	}
	switch p.Status {
	case domain.StatusPartiallyRefunded:
		_, err = s.orders.SetPaymentStatusTx(ctx, tx, p.OrderID, orders.PaymentPartiallyRefunded, actor)
	case domain.StatusRefunded:
		_, err = s.orders.SetPaymentStatusTx(ctx, tx, p.OrderID, orders.PaymentRefunded, actor)
	}
	return err
}

// autoRefundTx refunds a payment that should not have been taken.
func (s *Service) autoRefundTx(ctx context.Context, tx pgx.Tx, p domain.Payment, why string) error {
	requested, err := store.RequestedRefunds(ctx, tx, p.ID, p.Amount.Currency())
	if err != nil {
		return err
	}
	left, err := domain.Refundable(p, requested)
	if err != nil || left.IsZero() {
		return err
	}
	_, err = s.refundTx(ctx, tx, p, left, why)
	return err
}

// Refund asks the gateway to give back amountMinor (in the payment's
// currency) of a captured payment.
// What is asked is reserved at once; the gateway answers in a job.
func (s *Service) Refund(ctx context.Context, paymentID ulid.ID, amountMinor int64, reason string) (domain.Refund, error) {
	reason, err := domain.CleanReason(reason)
	if err != nil {
		return domain.Refund{}, err
	}
	var r domain.Refund
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		p, txErr := store.GetPayment(ctx, tx, paymentID, true)
		if txErr != nil {
			return txErr
		}
		if !p.Status.Captured() {
			return ErrNotRefundable
		}
		if amountMinor <= 0 {
			return errRefundTooMuch
		}
		amount, txErr := money.New(amountMinor, p.Amount.Currency())
		if txErr != nil {
			return txErr
		}
		requested, txErr := store.RequestedRefunds(ctx, tx, p.ID, p.Amount.Currency())
		if txErr != nil {
			return txErr
		}
		left, txErr := domain.Refundable(p, requested)
		if txErr != nil {
			return txErr
		}
		if amount.Amount() > left.Amount() {
			return errRefundTooMuch
		}
		r, txErr = s.refundTx(ctx, tx, p, amount, reason)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Refund{}, ErrPaymentNotFound
	}
	return r, err
}

func (s *Service) refundTx(ctx context.Context, tx pgx.Tx, p domain.Payment, amount money.Money, reason string) (domain.Refund, error) {
	r, err := store.InsertRefund(ctx, tx, domain.Refund{ID: s.ids.New(), PaymentID: p.ID, Amount: amount, Reason: reason})
	if err != nil {
		return domain.Refund{}, err
	}
	if err = s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return RefundPaymentArgs{TenantArgs: t, RefundID: r.ID} }, nil); err != nil {
		return domain.Refund{}, err
	}
	return r, s.audit.RecordTx(ctx, tx, audit.Entry{Action: "payment.refund_requested", TargetType: "payment", TargetID: p.ID.String(),
		Metadata: map[string]any{"refund_id": r.ID.String(), "amount_minor": amount.Amount()}})
}

// Payment returns a payment with its refunds.
func (s *Service) Payment(ctx context.Context, id ulid.ID) (domain.Payment, []domain.Refund, error) {
	var p domain.Payment
	var rs []domain.Refund
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if p, txErr = store.GetPayment(ctx, tx, id, false); txErr != nil {
			return txErr
		}
		rs, txErr = store.Refunds(ctx, tx, id)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Payment{}, nil, ErrPaymentNotFound
	}
	return p, rs, err
}

func (s *Service) payment(ctx context.Context, id ulid.ID) (domain.Payment, error) {
	p, _, err := s.Payment(ctx, id)
	return p, err
}

// OrderPayments returns an order's payments, newest first.
func (s *Service) OrderPayments(ctx context.Context, orderID ulid.ID) ([]domain.Payment, error) {
	var out []domain.Payment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.OrderPayments(ctx, tx, orderID)
		return txErr
	})
	return out, err
}

// Page is one page of payments, newest first.
type Page struct {
	Items []domain.Payment
	Next  *domain.Payment // the last item, when there are more
}

// Payments returns a page of the store's payments, newest first, before
// the (created at, id) of after when set.
func (s *Service) Payments(ctx context.Context, after *domain.Payment, limit int) (Page, error) {
	if limit == 0 {
		limit = DefaultPaymentPage
	}
	if limit < 1 || limit > MaxPaymentPage {
		return Page{}, errPageSize
	}
	beforeAt, beforeID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if after != nil {
		beforeAt, beforeID = after.CreatedAt, after.ID
	}
	var items []domain.Payment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		items, txErr = store.ListPayments(ctx, tx, beforeAt, beforeID, limit+1)
		return txErr
	})
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		p.Next = &p.Items[limit-1]
	}
	return p, nil
}

func (s *Service) enqueueTx(ctx context.Context, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	args := mk(t)
	if _, err = s.jobs.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("payments: enqueue %s: %w", args.Kind(), err)
	}
	return nil
}

func splitName(name string) (first, last string) {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], parts[0]
	}
	return parts[0], strings.Join(parts[1:], " ")
}
