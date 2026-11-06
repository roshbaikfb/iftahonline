package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
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

// The platform's checkout gateways take payments on their own page and
// save the card there: Paymob's (ADR-0053) and Stripe's (ADR-0091). An
// account opens its checkouts on its country's billing gateway
// (ADR-0089); each gateway's callbacks reach a URL of their own.

// CallbackProviderOf is the platform webhook path of gateway's
// callbacks: /v1/webhooks/<gateway>_billing.
func CallbackProviderOf(gateway string) string { return gateway + callbackSuffix }

const callbackSuffix = "_billing"

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
	// ClosesAt is when the storefront closes if still unpaid (ADR-0082);
	// Held is whether it has.
	ClosesAt time.Time
	Held     bool
	// Agency is a store an agency pays for: the platform switches it
	// (ADR-0087). Collection is how its account pays pay-as-you-sell fees
	// ("" until it chooses). Switch is the offer to move to pay as you
	// sell, while the store may (nil otherwise).
	Agency     bool
	Collection domain.Collection
	Switch     *SwitchOffer
	// SupportWhatsApp is the platform's WhatsApp number an agency's store
	// is sent to ("" for none, or not an agency's store).
	SupportWhatsApp string
	// A change waiting for the period's end (ADR-0060): the plan it moves
	// to (names empty for the free plan after a cancellation) and cycle.
	NextPlanNameAr, NextPlanNameEn string
	NextCycle                      domain.Cycle
	Card                           string // the card on file's label; "" without one
	OpenInvoices                   []store.OpenInvoiceRow
	// FoundingSeat and FoundingBps are a founding merchant's seat and
	// discount (ADR-0074); 0 when the store is not (or no longer) one.
	FoundingSeat int
	FoundingBps  int32
	// Gateway is where the store pays the platform: paymob or stripe, by
	// its country's payment rule (ADR-0089); "" without one.
	Gateway string
}

func (s *Service) checkoutGateway(name string) (CheckoutGateway, bool) {
	g, ok := s.gateways[name].(CheckoutGateway)
	return g, ok
}

// checkoutGateways are the names of the gateways with a checkout, sorted.
func (s *Service) checkoutGateways() []string {
	var out []string
	for name := range s.gateways {
		if _, ok := s.checkoutGateway(name); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// canCheckout reports whether any checkout can open: a gateway with one,
// and the platform's URL for its callbacks.
func (s *Service) canCheckout() bool { return s.publicURL != "" && len(s.checkoutGateways()) > 0 }

// billingGatewayTx names the gateway the store in ctx pays the platform
// on: its account's country's payment rule (ADR-0089), or the store's
// country's before it has an account; "" without a rule. tx is behind
// account when has.
func billingGatewayTx(ctx context.Context, tx pgx.Tx, account ulid.ID, has bool, info StoreInfo) (string, error) {
	country := info.country()
	if has {
		acct, err := store.Account(ctx, tx, account)
		if err != nil {
			return "", err
		}
		country = acct.Country
	}
	rules, err := store.PaymentRules(ctx, tx)
	if err != nil {
		return "", err
	}
	rule, ok := domain.RuleFor(rules, country)
	if !ok {
		return "", nil
	}
	return rule.BillingGateway, nil
}

// checkoutForTx is the gateway account opens its checkouts on: its
// country's billing gateway (ADR-0089), when the platform has it. tx is
// behind account.
func (s *Service) checkoutForTx(ctx context.Context, tx pgx.Tx, account ulid.ID) (string, CheckoutGateway, error) {
	name, err := billingGatewayTx(ctx, tx, account, true, StoreInfo{})
	if err != nil {
		return "", nil, err
	}
	gw, ok := s.checkoutGateway(name)
	if !ok {
		return "", nil, errCheckoutOff
	}
	return name, gw, nil
}

// openPage opens c's page on gw, where the merchant comes back to back
// (a path of the dashboard), and records the gateway's order and
// checkout.
func (s *Service) openPage(ctx context.Context, gw CheckoutGateway, c store.Checkout, description string, payer Payer, back string) (string, error) {
	page, err := gw.Checkout(ctx, CheckoutRequest{Reference: c.Reference, AmountMinor: c.AmountMinor, Currency: c.Currency,
		Description: description, Name: payer.Name, Email: payer.Email,
		NotificationURL: webhooks.PlatformURL(s.publicURL, CallbackProviderOf(c.Gateway)),
		RedirectURL:     strings.TrimRight(s.dashboardURL, "/") + back})
	if errors.Is(err, ErrNoCheckout) {
		return "", errCheckoutOff
	}
	if err != nil {
		return "", err
	}
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := store.SetCheckoutOrder(ctx, tx, c.Reference, page.ProviderOrderID); txErr != nil {
			return txErr
		}
		return store.SetCheckoutSession(ctx, tx, c.Reference, page.Session)
	}); err != nil {
		return "", err
	}
	return page.URL, nil
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
	info, infoErr := s.storeInfo(ctx)
	if infoErr != nil {
		return out, infoErr
	}
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
		out.ClosesAt, out.Held = sub.ClosesAt(s.policy), sub.Held()
		if !sub.NextPlanID.IsZero() {
			if out.NextPlanNameAr, out.NextPlanNameEn, err = store.PlanNames(ctx, tx, sub.NextPlanID); err != nil {
				return err
			}
		}
		fm, isMember, err := store.FoundingMember(ctx, tx)
		if err != nil {
			return err
		}
		if isMember && fm.Active() {
			out.FoundingSeat, out.FoundingBps = fm.Seat, fm.DiscountBps
		}
		out.Agency = sub.Kind == domain.KindAgency
		account, has, err := accountOf(ctx, tx)
		if err != nil {
			return err
		}
		if has {
			if err = store.UseAccount(ctx, tx, account); err != nil {
				return err
			}
			m, hasCard, mErr := store.PaymentMethod(ctx, tx, account)
			if mErr != nil {
				return mErr
			}
			if hasCard {
				out.Card = m.Label
			}
			b, bErr := store.AccountBalance(ctx, tx, account)
			if bErr != nil {
				return bErr
			}
			out.Agency, out.Collection = out.Agency || b.Kind == domain.AccountAgency, b.Collection
			if out.OpenInvoices, err = store.OpenInvoices(ctx, tx, account); err != nil {
				return err
			}
		}
		if out.Gateway, err = billingGatewayTx(ctx, tx, account, has, info); err != nil {
			return err
		}
		if ok && !out.Agency && sub.CanSwitchToPays() {
			cur, cErr := billedTx(ctx, tx, known(info))
			if cErr != nil {
				return cErr
			}
			out.Switch, err = switchOffer(ctx, tx, cur)
		}
		return err
	})
	if err == nil && out.Agency && s.support != nil {
		// Best effort: the banner shows no button without it.
		if n, sErr := s.support(ctx); sErr == nil {
			out.SupportWhatsApp = n
		} else {
			s.logf(ctx, "billing: the platform's WhatsApp number", sErr)
		}
	}
	return out, err
}

// SwitchOffer is what moving to pay as you sell costs and how its fees
// may be paid (ADR-0087).
type SwitchOffer struct {
	Currency   string              // the one the store is billed in
	RateBps    int32               // the fee on delivered orders' goods
	FixedMinor int64               // and the fixed amount per order, in Currency (ADR-0090)
	Offered    []domain.Collection // the ways the platform offers now
}

// switchOffer is the offer in currency; nil when pay as you sell is not
// on sale, charges nothing in it, or no way of paying is offered.
func switchOffer(ctx context.Context, tx pgx.Tx, currency string) (*SwitchOffer, error) {
	pays, err := paysPlan(ctx, tx)
	if errors.Is(err, errNoPaysPlan) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rule, ok, err := store.FeeRule(ctx, tx, pays.ID, currency)
	if err != nil || !ok {
		return nil, err
	}
	settings, err := store.CollectionSettings(ctx, tx)
	if err != nil || len(settings.Offered()) == 0 {
		return nil, err
	}
	return &SwitchOffer{Currency: currency, RateBps: rule.RateBps, FixedMinor: rule.FixedMinor, Offered: settings.Offered()}, nil
}

// Payer is who pays a checkout, as the gateway asks.
type Payer struct {
	Name, Email string
}

// PayNow opens the gateway's page for an open invoice of the store in
// ctx's account, where the card is also saved for the next ones
// (ADR-0053), on its country's gateway (ADR-0091). The callbacks settle
// it.
func (s *Service) PayNow(ctx context.Context, invoice ulid.ID, payer Payer) (string, error) {
	if !s.canCheckout() {
		return "", errCheckoutOff
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return "", err
	}
	c := store.Checkout{ID: s.ids.New(), Invoice: invoice}
	c.Reference = "bc_" + c.ID.String()
	var number string
	var gw CheckoutGateway
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
		if c.Gateway, gw, txErr = s.checkoutForTx(ctx, tx, account); txErr != nil {
			return txErr
		}
		c.Account, c.Currency, c.AmountMinor, number = account, inv.Currency, inv.TotalMinor, inv.Number
		return store.InsertCheckout(ctx, tx, c)
	})
	if err != nil {
		return "", err
	}
	return s.openPage(ctx, gw, c, number, payer, "/stores/"+storeID.String()+"/billing")
}

// CallbackPlatform is billing's side of the webhooks inbox for one
// checkout gateway: one URL for the platform's accounts on it, every
// store's checkouts.
type CallbackPlatform struct {
	svc     *Service
	gateway string
}

var _ webhooks.PlatformProvider = CallbackPlatform{}

// CallbackProviders are billing's platform webhook providers, one per
// checkout gateway (none without one).
func (s *Service) CallbackProviders() []webhooks.PlatformProvider {
	names := s.checkoutGateways()
	out := make([]webhooks.PlatformProvider, len(names))
	for i, name := range names {
		out[i] = CallbackPlatform{svc: s, gateway: name}
	}
	return out
}

// Name implements webhooks.PlatformProvider.
func (p CallbackPlatform) Name() string { return CallbackProviderOf(p.gateway) }

// VerifyPlatform implements webhooks.PlatformProvider: the gateway
// checks the signature, and the callback is placed in the store of its
// checkout, by our reference or the gateway's order. A callback of no
// checkout is dropped with a log line.
func (p CallbackPlatform) VerifyPlatform(ctx context.Context, req webhooks.Request) ([]webhooks.Part, error) {
	gw, _ := p.svc.checkoutGateway(p.gateway)
	cb, err := gw.ParseCallback(req.Header, req.Query, req.Body)
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

// ChallengePlatform implements webhooks.PlatformProvider: neither
// gateway has a URL handshake.
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
//   - a saved card: it becomes the account's card on file (Paymob says
//     so in a callback of its own, Stripe in its answer about the
//     payment).
//
// The receipt's provider names the gateway; a callback placed in a
// checkout opened on another one is not acted on.
func (s *Service) ProcessCallback(ctx context.Context, receipt ulid.ID) error {
	if s.inbox == nil {
		return errors.New("billing: no inbox")
	}
	r, _, body, err := s.inbox.Get(ctx, receipt)
	if errors.Is(err, webhooks.ErrReceiptNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	gateway, _ := strings.CutSuffix(r.Provider, callbackSuffix)
	gw, ok := s.checkoutGateway(gateway)
	if !ok {
		return fmt.Errorf("billing: no checkout gateway %q", gateway)
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
	if c.Gateway != gateway {
		return s.markReceipt(ctx, receipt, "other_gateway")
	}
	if cb.Kind == CallbackToken {
		if err = s.SavePaymentMethod(ctx, c.Account, c.Gateway, cb.Token, cb.Label); err != nil {
			return err
		}
		return s.markReceipt(ctx, receipt, "card_saved")
	}
	pay, err := gw.Paid(ctx, PaidQuery{Currency: c.Currency, Reference: c.Reference, Session: c.Session, AmountMinor: c.AmountMinor})
	if err != nil {
		return err
	}
	if !pay.Paid {
		return s.finishCheckout(ctx, receipt, c, "failed", "not_paid")
	}
	if pay.Token != "" {
		// Saved before the payment is recorded: a failure retries both.
		if err = s.SavePaymentMethod(ctx, c.Account, c.Gateway, pay.Token, pay.Label); err != nil {
			return err
		}
	}
	txn := pay.TransactionID
	if c.TopUp() {
		return s.toppedUp(ctx, receipt, c, txn)
	}
	duplicate, err := s.recordAttempt(ctx, c.Account, store.Attempt{ID: s.ids.New(), Invoice: c.Invoice, Account: c.Account,
		Gateway: c.Gateway, Outcome: "approved", Reference: txn, AmountMinor: c.AmountMinor})
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
