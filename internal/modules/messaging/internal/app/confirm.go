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
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// StoreInfo is what a confirmation needs about the store in ctx.
type StoreInfo struct {
	Name   string
	Locale string // "ar" or "en"
	// Host is the store's primary host, for the SMS link.
	Host string
}

// StoreSource returns the StoreInfo of the store in ctx; cmd/platform
// adapts the tenancy module to it.
type StoreSource func(ctx context.Context) (StoreInfo, error)

// Contacts returns the email addresses of the store's owner and
// managers, for the notices they get by email; cmd/platform adapts the
// identity module to it.
type Contacts func(ctx context.Context) ([]string, error)

// Confirmations are the order confirmation's collaborators: the worker
// role has them all; the api role none (it only serves settings and
// notices).
type Confirmations struct {
	// Number is the platform's WhatsApp number in E.164, which customers
	// may write to first (the thank-you page's link, ADR-0041); "" turns
	// that off.
	Number       string
	Orders       orders.Fulfilment
	Stores       StoreSource
	Contacts     Contacts
	Mailer       mailer.Mailer
	DashboardURL string
}

// ready reports whether orders can be read and moved (the worker and
// the storefront roles).
func (c Confirmations) ready() bool { return c.Orders != nil }

var errNoOrders = errors.New("messaging: order confirmation needs the orders (and, to ask, the stores): worker role")

// ConfirmOrder handles orders.order.placed: a cash-on-delivery order
// still pending, in a store that confirms, gets its confirmation and
// message in one transaction, and its deadline job. Redeliveries of the
// event do nothing (one confirmation per order).
//
// What the risk rules did must not show to whoever placed the order
// (ADR-0070): where the store confirms, every order gets a confirmation
// (and the thank-you page its WhatsApp link). A held one is asked
// nothing (the merchant reviews it); a prepaid delivery is sent the link
// to pay it, to the phone, whether or not the store confirms; the extra
// confirmation asks even a store that does not confirm.
func (s *Service) ConfirmOrder(ctx context.Context, orderID ulid.ID) error {
	if !s.conf.ready() || s.conf.Stores == nil {
		return errNoOrders
	}
	o, err := s.conf.Orders.Get(ctx, orderID)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if o.PaymentMethod != orders.PaymentCOD || o.Status != orders.StatusPending {
		return nil
	}
	info, err := s.conf.Stores(ctx)
	if err != nil {
		return err
	}
	locale := info.Locale
	if !domain.ValidLocale(locale) {
		locale = domain.Arabic
	}
	prepay := o.RiskAction == orders.RiskPrepay && o.DepositStatus == orders.PaymentAwaiting
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		set, err := store.Settings(ctx, tx)
		if err != nil {
			return err
		}
		if !set.ConfirmCOD && !prepay && o.RiskAction != orders.RiskVerify {
			return nil
		}
		token, hash, err := domain.NewToken()
		if err != nil {
			return err
		}
		deadline := o.CreatedAt.Add(time.Duration(set.AutoCancelHours) * time.Hour)
		if earliest := s.now().Add(time.Minute); deadline.Before(earliest) {
			deadline = earliest
		}
		c := domain.Confirmation{OrderID: o.ID, Status: domain.Pending, ExpiresAt: deadline}
		created, err := s.insertConfirmation(ctx, tx, &c, hash)
		if err != nil || !created || o.RiskAction == orders.RiskHold {
			return err
		}
		ml := money.LocaleEN
		if locale == domain.Arabic {
			ml = money.LocaleAR
		}
		out := Outgoing{Kind: domain.OrderConfirmation, Locale: locale, OrderID: &o.ID, To: o.Contact.Phone,
			Vars: domain.Vars{CustomerName: o.Contact.Name, StoreName: info.Name, OrderRef: o.Code(), Total: o.Total.Format(ml),
				ConfirmURL: "https://" + info.Host + domain.ConfirmPath(locale, token)}}
		switch {
		case prepay:
			// The payment confirms the order, and payments cancels it
			// unpaid: no deadline here.
			out.Kind, out.Vars.Due = domain.DeliveryPrepay, o.Deposit.Format(ml)
		case c.WACode != "" && set.ConfirmCOD:
			// The customer may confirm for free from the thank-you page
			// first: the paid template waits.
			out.SendAt = s.now().Add(CustomerFirst)
		}
		q, err := s.EnqueueTx(ctx, tx, out)
		if err != nil {
			return err
		}
		c.MessageID = &q.ID
		switch q.Status {
		case domain.Queued:
			if !prepay {
				if err = s.enqueueTx(ctx, tx, AutoCancelArgs{OrderID: o.ID}, &river.InsertOpts{ScheduledAt: deadline,
					UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
					return err
				}
			}
		case domain.Skipped:
			c.Status = domain.QuotaSkipped
		default: // no channel: the merchant confirms by hand, as before
			c.Status = domain.Unreached
		}
		if err = store.UpdateConfirmation(ctx, tx, c); err != nil {
			return err
		}
		return s.quotaNoticeTx(ctx, tx, q)
	})
}

// CustomerFirst is how long the paid confirmation template waits for a
// customer who can confirm for free from the thank-you page (ADR-0041).
const CustomerFirst = 10 * time.Minute

// codeTries bounds drawing a WhatsApp code that no other order holds
// (a clash is about one in 10^12 per draw).
const codeTries = 3

// insertConfirmation opens c, with a WhatsApp code when the platform has
// a number to confirm on. Each draw runs in a savepoint, so a clash with
// another store's code (a unique violation) leaves tx usable.
func (s *Service) insertConfirmation(ctx context.Context, tx pgx.Tx, c *domain.Confirmation, hash []byte) (bool, error) {
	for range codeTries {
		code := ""
		if s.conf.Number != "" && s.channels.WhatsApp != nil {
			var err error
			if code, err = domain.NewCode(); err != nil {
				return false, err
			}
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return false, fmt.Errorf("messaging: savepoint: %w", err)
		}
		created, err := store.InsertConfirmation(ctx, sp, *c, hash, code)
		if errors.Is(err, store.ErrCodeTaken) {
			if err = sp.Rollback(ctx); err != nil {
				return false, err
			}
			continue
		}
		if err != nil {
			_ = sp.Rollback(ctx) //nolint:errcheck // the insert's error is the one to report
			return false, err
		}
		c.WACode = code
		return created, sp.Commit(ctx)
	}
	return false, errors.New("messaging: no free WhatsApp code")
}

// quotaNoticeTx warns the merchant when a message crossed 80% or 100% of
// the quota, or was skipped for it: once per month and threshold.
func (s *Service) quotaNoticeTx(ctx context.Context, tx pgx.Tx, q Queued) error {
	kind := domain.NoticeKind("")
	switch {
	case q.Status == domain.Skipped || q.Usage.CrossedBy(1) == entitlements.Reached:
		kind = domain.QuotaReached
	case q.Usage.CrossedBy(1) == entitlements.Nearly:
		kind = domain.QuotaNearly
	default:
		return nil
	}
	return s.notifyTx(ctx, tx, domain.QuotaNotice(kind, q.Usage.Period, q.Usage.Used, q.Usage.Limit))
}

// PlanLimitCrossed warns the merchant that a monthly limit of the store's
// plan is near or reached (entitlements.UsageCrossed), once per feature,
// month and threshold: in the bell, and by email.
func (s *Service) PlanLimitCrossed(ctx context.Context, ev entitlements.UsageCrossed) error {
	kind := domain.QuotaNearly
	switch ev.Threshold {
	case entitlements.Reached:
		kind = domain.QuotaReached
	case entitlements.Nearly:
	default:
		return nil
	}
	n := domain.PlanLimitNotice(kind, string(ev.Feature), ev.Period, ev.Used, ev.Limit)
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return s.notifyTx(ctx, tx, n) })
}

// notifyTx adds a notice for the store's staff, once; an emailed kind
// gets its email job.
func (s *Service) notifyTx(ctx context.Context, tx pgx.Tx, n domain.Notification) error {
	n.ID = s.ids.New()
	created, err := store.InsertNotification(ctx, tx, n)
	if err != nil || !created || !n.Kind.Emailed() {
		return err
	}
	return s.enqueueTx(ctx, tx, EmailNoticeArgs{NotificationID: n.ID}, nil)
}

// HandleReplyTx implements ReplyHandler: a customer's answer to an order
// confirmation moves the order, in the transaction that records it.
func (s *Service) HandleReplyTx(ctx context.Context, tx pgx.Tx, r Reply) error {
	if r.Kind != domain.OrderConfirmation || r.OrderID == nil {
		return nil
	}
	settled, moved, err := s.AnswerTx(ctx, tx, *r.OrderID, r.Answer, domain.ActorWhatsApp, r.At)
	if err != nil || !moved {
		return err
	}
	return s.ackTx(ctx, tx, *r.OrderID, settled)
}

// ackTx thanks a customer who answered on WhatsApp, in the conversation
// they opened (free, no quota): what became of their order. Without
// WhatsApp or the stores (not the worker), nothing is sent.
func (s *Service) ackTx(ctx context.Context, tx pgx.Tx, orderID ulid.ID, settled domain.ConfirmationStatus) error {
	if s.channels.WhatsApp == nil || s.conf.Stores == nil {
		return nil
	}
	o, err := s.conf.Orders.GetTx(ctx, tx, orderID)
	if err != nil {
		return err
	}
	info, err := s.conf.Stores(ctx)
	if err != nil {
		return err
	}
	locale := info.Locale
	if !domain.ValidLocale(locale) {
		locale = domain.Arabic
	}
	outcome := string(settled)
	switch {
	case awaitingCall(settled, o):
		outcome = domain.OutcomeAwaitingCall
	case settled == domain.Pending && o.DepositStatus == orders.PaymentAwaiting:
		outcome = domain.OutcomeDepositDue
	}
	_, err = s.EnqueueTx(ctx, tx, Outgoing{Kind: domain.AnswerAck, Locale: locale, OrderID: &orderID, To: o.Contact.Phone,
		Vars: domain.Vars{CustomerName: o.Contact.Name, StoreName: info.Name, OrderRef: o.Code(), Outcome: outcome}})
	return err
}

// AnswerTx applies a customer's answer to an order's confirmation: the
// order is confirmed or cancelled on the customer's behalf (actor), and
// the merchant told. An answer to a confirmation already settled, or to
// an order the merchant moved, changes nothing (the state it settled
// in is returned).
// It reports whether this answer settled the confirmation (moved).
func (s *Service) AnswerTx(ctx context.Context, tx pgx.Tx, orderID ulid.ID, answer domain.Reply, actor string, at time.Time) (
	settled domain.ConfirmationStatus, moved bool, err error,
) {
	if !s.conf.ready() {
		return "", false, errNoOrders
	}
	c, err := store.Confirmation(ctx, tx, orderID, true)
	if errors.Is(err, store.ErrConfirmationNotFound) {
		return "", false, ErrConfirmationNotFound
	}
	// An unreached customer who has the message after all may answer.
	if err != nil || (c.Status != domain.Pending && c.Status != domain.Unreached) {
		return c.Status, false, err
	}
	to, notice, verb := orders.StatusConfirmed, domain.OrderConfirmedNotice, "confirmed"
	settled = domain.Confirmed
	if answer == domain.Cancel {
		to, settled, notice, verb = orders.StatusCancelled, domain.Cancelled, domain.OrderCancelledNotice, "cancelled"
	}
	_, err = s.conf.Orders.TransitionSystemTx(ctx, tx, orderID, to, actor, "the customer "+verb+" by "+strings.TrimPrefix(actor, "customer:"))
	needsCall := false
	switch {
	case errors.Is(err, orders.ErrNeedsCall), errors.Is(err, orders.ErrOrderHeld):
		// The risk rules want this yes verified by phone, or the order
		// reviewed (ADR-0066): the answer is kept, the order stays
		// pending for the merchant.
		needsCall, err = true, nil
	case errors.Is(err, orders.ErrDepositDue):
		// Paying the delivery confirms it (ADR-0070); the confirmation
		// stays open for the payment, or a cancel.
		return domain.Pending, true, nil
	case errors.Is(err, orders.ErrInvalidTransition):
		settled, err = domain.Stale, nil
	}
	if err != nil {
		return "", false, err
	}
	c.Status, c.AnsweredAt, c.AnsweredBy = settled, &at, actor
	if err = store.UpdateConfirmation(ctx, tx, c); err != nil {
		return "", false, err
	}
	if settled == domain.Stale {
		return settled, true, nil
	}
	n := domain.OrderNotice(notice, orderID, orders.CodeOf(orderID))
	n.Data.NeedsCall = needsCall
	return settled, true, s.notifyTx(ctx, tx, n)
}

// ErrConfirmationNotFound means the order asked for no confirmation.
var ErrConfirmationNotFound = errs.NotFound("confirmation_not_found", "this order has no confirmation")

// AutoCancelArgs is an order's deadline to be confirmed.
type AutoCancelArgs struct {
	jobs.TenantArgs
	OrderID ulid.ID `json:"order_id"`
}

// Kind implements river.JobArgs.
func (AutoCancelArgs) Kind() string { return "messaging.auto_cancel_order" }

// reached reports whether a message got to the customer's phone (or at
// least to the channel, which delivers it).
func reached(st domain.Status) bool {
	return st == domain.Sent || st == domain.Delivered || st == domain.Read
}

// AutoCancel runs AutoCancelArgs. A confirmation still pending whose
// message reached the customer cancels its order (stock comes back
// through orders); one whose message never did leaves the order to the
// merchant, who is told. An order the merchant already moved is left
// alone.
func (s *Service) AutoCancel(ctx context.Context, orderID ulid.ID) error {
	if !s.conf.ready() {
		return errNoOrders
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := store.Confirmation(ctx, tx, orderID, true)
		if errors.Is(err, store.ErrConfirmationNotFound) {
			return nil
		}
		if err != nil || c.Status != domain.Pending {
			return err
		}
		ref := orders.CodeOf(orderID)
		now := s.now().UTC()
		var m domain.Message
		if c.MessageID != nil {
			if m, err = store.Get(ctx, tx, *c.MessageID, false); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		if !reached(m.Status) {
			c.Status = domain.Unreached
			if err = store.UpdateConfirmation(ctx, tx, c); err != nil {
				return err
			}
			return s.notifyTx(ctx, tx, domain.OrderNotice(domain.ConfirmationUnreached, orderID, ref))
		}
		_, err = s.conf.Orders.TransitionSystemReasonTx(ctx, tx, orderID, orders.StatusCancelled, domain.ActorAutoCancel, "not confirmed in time",
			orders.ReasonUnconfirmed)
		settled := domain.Expired
		if errors.Is(err, orders.ErrInvalidTransition) {
			settled, err = domain.Stale, nil
		}
		if err != nil {
			return err
		}
		c.Status, c.AnsweredAt, c.AnsweredBy = settled, &now, domain.ActorAutoCancel
		if err = store.UpdateConfirmation(ctx, tx, c); err != nil || settled == domain.Stale {
			return err
		}
		return s.notifyTx(ctx, tx, domain.OrderNotice(domain.OrderAutoCancelled, orderID, ref))
	})
}

// EmailNoticeArgs emails a notice to the store's owner and managers.
type EmailNoticeArgs struct {
	jobs.TenantArgs
	NotificationID ulid.ID `json:"notification_id"`
}

// Kind implements river.JobArgs.
func (EmailNoticeArgs) Kind() string { return "messaging.email_notice" }

// EmailNotice runs EmailNoticeArgs. Without a mailer or contacts it does
// nothing; the notice is in the dashboard anyway.
func (s *Service) EmailNotice(ctx context.Context, id ulid.ID) error {
	if s.conf.Mailer == nil || s.conf.Contacts == nil || s.conf.Stores == nil {
		return nil
	}
	var n domain.Notification
	if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		n, err = store.Notification(ctx, tx, id)
		return err
	}); errors.Is(err, domain.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	info, err := s.conf.Stores(ctx)
	if err != nil {
		return err
	}
	to, err := s.conf.Contacts(ctx)
	if err != nil {
		return err
	}
	subject, text := n.Email(info.Locale, info.Name, strings.TrimRight(s.conf.DashboardURL, "/"))
	for _, addr := range to {
		if err = s.conf.Mailer.Send(ctx, mailer.Message{To: addr, Subject: subject, Text: text}); err != nil {
			return fmt.Errorf("messaging: email notice: %w", err)
		}
	}
	return nil
}

// enqueueTx inserts a job of the store in ctx.
func (s *Service) enqueueTx(ctx context.Context, tx pgx.Tx, args interface {
	river.JobArgs
	withTenant(jobs.TenantArgs) river.JobArgs
}, opts *river.InsertOpts) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	a := args.withTenant(t)
	if _, err = s.jobs.InsertTx(ctx, tx, a, opts); err != nil {
		return fmt.Errorf("messaging: enqueue %s: %w", a.Kind(), err)
	}
	return nil
}

func (a AutoCancelArgs) withTenant(t jobs.TenantArgs) river.JobArgs  { a.TenantArgs = t; return a }
func (a EmailNoticeArgs) withTenant(t jobs.TenantArgs) river.JobArgs { a.TenantArgs = t; return a }
