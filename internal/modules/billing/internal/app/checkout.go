package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CheckoutGatewayName is the gateway that takes payments on its own page
// and saves cards there.
const CheckoutGatewayName = "paymob"

// CallbackProvider is the platform webhook path of its callbacks:
// /v1/webhooks/paymob_billing.
const CallbackProvider = "paymob_billing"

// BillingConnection is the connection ID billing's callback receipts
// carry: the platform's own account, no store connection.
func BillingConnection() ulid.ID { return ulid.ID{15: 2} }

// Errors the HTTP layer renders.
var (
	ErrInvoiceNotFound = errs.NotFound("invoice_not_found", "invoice not found")
	errCheckoutOff     = errs.Unavailable(time.Minute, ErrNoCheckout)
)

// Summary is the store's billing as its owner sees it.
type Summary struct {
	PlanNameAr, PlanNameEn string
	Status                 domain.Status
	Cycle                  domain.Cycle
	TrialEndsAt            time.Time
	PeriodEnd              time.Time
	GraceUntil             time.Time
	CancelAtPeriodEnd      bool
	// A change waiting for the period's end (ADR-0060): the plan it moves
	// to (names empty for the free plan after a cancellation) and cycle.
	NextPlanNameAr, NextPlanNameEn string
	NextCycle                      domain.Cycle
	Card                           string // the card on file's label; "" without one
	OpenInvoices                   []store.OpenInvoiceRow
}

func (s *Service) checkout() (CheckoutGateway, bool) {
	g, ok := s.gateways[CheckoutGatewayName].(CheckoutGateway)
	return g, ok
}

// accountOf is the store-in-ctx's account; ok is false before its first
// invoice.
func accountOf(ctx context.Context, tx pgx.Tx) (ulid.ID, bool, error) {
	id, ok, err := store.SubscriptionAccount(ctx, tx)
	if errors.Is(err, store.ErrNoSubscription) {
		return ulid.ID{}, false, nil
	}
	return id, ok, err
}

// Summary reads the store-in-ctx's plan, its card on file and its open
// invoices.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	var out Summary
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		sub, ok, err := store.Subscription(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			free, fErr := store.FreePlan(ctx, tx)
			if fErr != nil {
				return fErr
			}
			sub = domain.OnFree(free)
		}
		if out.PlanNameAr, out.PlanNameEn, err = store.PlanNames(ctx, tx, sub.PlanID); err != nil {
			return err
		}
		out.Status, out.Cycle, out.TrialEndsAt, out.PeriodEnd = sub.Status, sub.Cycle, sub.TrialEndsAt, sub.PeriodEnd
		out.GraceUntil, out.CancelAtPeriodEnd, out.NextCycle = sub.GraceUntil, sub.CancelAtPeriodEnd, sub.NextCycle
		if !sub.NextPlanID.IsZero() {
			if out.NextPlanNameAr, out.NextPlanNameEn, err = store.PlanNames(ctx, tx, sub.NextPlanID); err != nil {
				return err
			}
		}
		account, has, err := accountOf(ctx, tx)
		if err != nil || !has {
			return err
		}
		if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		m, hasCard, err := store.PaymentMethod(ctx, tx, account)
		if err != nil {
			return err
		}
		if hasCard {
			out.Card = m.Label
		}
		out.OpenInvoices, err = store.OpenInvoices(ctx, tx, account)
		return err
	})
	return out, err
}

// Payer is who pays a checkout, as the gateway asks.
type Payer struct {
	Name, Email string
}

// PayNow opens the gateway's page for an open invoice of the store in
// ctx's account, where the card is also saved for the next ones
// (ADR-0053). The callbacks settle it.
func (s *Service) PayNow(ctx context.Context, invoice ulid.ID, payer Payer) (string, error) {
	gw, ok := s.checkout()
	if !ok || s.callbackURL == "" {
		return "", errCheckoutOff
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return "", err
	}
	c := store.Checkout{ID: s.ids.New(), Invoice: invoice}
	c.Reference = "bc_" + c.ID.String()
	var number string
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		account, has, txErr := accountOf(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if !has {
			return ErrInvoiceNotFound
		}
		if txErr = store.UseAccount(ctx, tx, account); txErr != nil {
			return txErr
		}
		inv, open, txErr := store.LockOpenInvoice(ctx, tx, invoice)
		if txErr != nil {
			return txErr
		}
		if !open {
			return ErrInvoiceNotFound
		}
		c.Account, c.Currency, c.AmountMinor, number = account, inv.Currency, inv.TotalMinor, inv.Number
		return store.InsertCheckout(ctx, tx, c)
	})
	if err != nil {
		return "", err
	}
	page, err := gw.Checkout(ctx, CheckoutRequest{Reference: c.Reference, AmountMinor: c.AmountMinor, Currency: c.Currency,
		Description: number, Name: payer.Name, Email: payer.Email, NotificationURL: s.callbackURL,
		RedirectURL: strings.TrimRight(s.dashboardURL, "/") + "/stores/" + storeID.String() + "/billing"})
	if errors.Is(err, ErrNoCheckout) {
		return "", errCheckoutOff
	}
	if err != nil {
		return "", err
	}
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		return store.SetCheckoutOrder(ctx, tx, c.Reference, page.ProviderOrderID)
	}); err != nil {
		return "", err
	}
	return page.URL, nil
}

// CallbackPlatform is billing's side of the webhooks inbox: one URL for
// the platform's Paymob accounts, every store's checkouts.
type CallbackPlatform struct{ svc *Service }

var _ webhooks.PlatformProvider = CallbackPlatform{}

// CallbackProvider returns billing's platform webhook provider; ok is
// false without a checkout gateway.
func (s *Service) CallbackProvider() (webhooks.PlatformProvider, bool) {
	if _, ok := s.checkout(); !ok {
		return nil, false
	}
	return CallbackPlatform{svc: s}, true
}

// Name implements webhooks.PlatformProvider.
func (CallbackPlatform) Name() string { return CallbackProvider }

// VerifyPlatform implements webhooks.PlatformProvider: the gateway
// checks the signature, and the callback is placed in the store of its
// checkout, by our reference or the gateway's order. A callback of no
// checkout is dropped with a log line.
func (p CallbackPlatform) VerifyPlatform(ctx context.Context, req webhooks.Request) ([]webhooks.Part, error) {
	gw, _ := p.svc.checkout()
	cb, err := gw.ParseCallback(req.Query, req.Body)
	if errors.Is(err, ErrForgedCallback) {
		return nil, webhooks.ErrForged
	}
	if err != nil {
		p.svc.logf(ctx, "billing: unreadable callback", err)
		return nil, nil
	}
	var storeID ulid.ID
	var found bool
	if err = postgres.InTx(ctx, p.svc.db, func(tx pgx.Tx) (txErr error) {
		storeID, found, txErr = store.CheckoutStore(ctx, tx, cb.Reference, cb.ProviderOrderID)
		return txErr
	}); err != nil {
		return nil, err
	}
	if !found {
		p.svc.logf(ctx, "billing: callback of no checkout", errors.New(cb.EventID))
		return nil, nil
	}
	return []webhooks.Part{{Store: tenant.StoreID{ID: storeID}, ConnectionID: BillingConnection(), EventID: cb.EventID, Body: req.Body}}, nil
}

// ChallengePlatform implements webhooks.PlatformProvider: Paymob has no
// URL handshake.
func (CallbackPlatform) ChallengePlatform(context.Context, url.Values) (string, error) {
	return "", webhooks.ErrForged
}

// ProcessCallbackArgs processes an accepted callback.
type ProcessCallbackArgs struct {
	jobs.TenantArgs
	ReceiptID ulid.ID `json:"receipt_id"`
}

// Kind implements river.JobArgs.
func (ProcessCallbackArgs) Kind() string { return "billing.process_callback" }

// AcceptTx implements webhooks.PlatformProvider.
func (p CallbackPlatform) AcceptTx(ctx context.Context, tx pgx.Tx, r webhooks.Receipt) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	_, err = p.svc.jobs.InsertTx(ctx, tx, ProcessCallbackArgs{TenantArgs: t, ReceiptID: r.ID}, nil)
	return err
}

// ProcessCallback acts on a checkout's callback, for the store in ctx:
//   - a payment: the gateway is asked whether the checkout is paid in
//     full (the callback is a hint), and only then is the invoice
//     settled;
//   - a saved card: it becomes the account's card on file.
func (s *Service) ProcessCallback(ctx context.Context, receipt ulid.ID) error {
	gw, ok := s.checkout()
	if !ok || s.inbox == nil {
		return errors.New("billing: no checkout gateway or inbox")
	}
	_, _, body, err := s.inbox.Get(ctx, receipt)
	if errors.Is(err, webhooks.ErrReceiptNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	cb, err := gw.ReadCallback(body)
	if body == nil || err != nil {
		return s.markReceipt(ctx, receipt, "unreadable")
	}
	var c store.Checkout
	var found bool
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if c, found, txErr = store.FindCheckout(ctx, tx, cb.Reference, cb.ProviderOrderID); txErr != nil || !found {
			return txErr
		}
		if cb.Kind == CallbackTransaction {
			return store.SetCheckoutOrder(ctx, tx, c.Reference, cb.ProviderOrderID)
		}
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return s.markReceipt(ctx, receipt, "unplaced")
	}
	if cb.Kind == CallbackToken {
		if err = s.SavePaymentMethod(ctx, c.Account, CheckoutGatewayName, cb.Token, cb.Label); err != nil {
			return err
		}
		return s.markReceipt(ctx, receipt, "card_saved")
	}
	txn, paid, err := gw.Paid(ctx, c.Currency, c.Reference, c.AmountMinor)
	if err != nil {
		return err
	}
	if !paid {
		return s.finishCheckout(ctx, receipt, c, "failed", "not_paid")
	}
	duplicate, err := s.recordAttempt(ctx, c.Account, store.Attempt{ID: s.ids.New(), Invoice: c.Invoice, Account: c.Account,
		Gateway: CheckoutGatewayName, Outcome: "approved", Reference: txn, AmountMinor: c.AmountMinor})
	if err != nil {
		return err
	}
	if duplicate { // the invoice was paid meanwhile: kept for a refund (ADR-0063)
		return s.finishCheckout(ctx, receipt, c, "paid", "duplicate")
	}
	return s.finishCheckout(ctx, receipt, c, "paid", "paid")
}

func (s *Service) finishCheckout(ctx context.Context, receipt ulid.ID, c store.Checkout, status, outcome string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SetCheckoutStatus(ctx, tx, c.ID, status); err != nil {
			return err
		}
		_, err := s.inbox.MarkProcessedTx(ctx, tx, receipt, outcome)
		return err
	})
}

func (s *Service) markReceipt(ctx context.Context, receipt ulid.ID, outcome string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := s.inbox.MarkProcessedTx(ctx, tx, receipt, outcome)
		return err
	})
}

func (s *Service) logf(ctx context.Context, msg string, err error) {
	if s.log != nil {
		s.log.InfoContext(ctx, msg, "err", err)
	}
}

// ProcessCallbackWorker runs ProcessCallbackArgs.
type ProcessCallbackWorker struct {
	river.WorkerDefaults[ProcessCallbackArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *ProcessCallbackWorker) Work(ctx context.Context, j *river.Job[ProcessCallbackArgs]) error {
	return w.Svc.ProcessCallback(ctx, j.Args.ReceiptID)
}

// CallbackURL is the platform's URL of billing's callbacks.
func CallbackURL(publicURL string) string {
	return fmt.Sprintf("%s/v1/webhooks/%s", strings.TrimRight(publicURL, "/"), CallbackProvider)
}
