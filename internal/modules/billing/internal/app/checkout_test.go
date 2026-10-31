package app_test

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// checkoutGateway is a fake gateway's page and callbacks: a callback body
// is "<kind> <reference> <order> <token>", signed when the query has
// hmac=ok; paid lists the references Paymob would say are paid.
type checkoutGateway struct {
	*fakeGateway
	pages []app.CheckoutRequest
	paid  map[string]bool
	base  int // Paymob's order numbers start after it (4546 when zero): one per test in the shared database
}

func storeOf(ctx context.Context) [16]byte {
	id, _ := tenant.FromContext(ctx)
	return id.ID
}

func (g *checkoutGateway) Checkout(_ context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	if c.Currency != "EGP" {
		return app.CheckoutPage{}, app.ErrNoCheckout
	}
	g.pages = append(g.pages, c)
	base := g.base
	if base == 0 {
		base = 4546
	}
	return app.CheckoutPage{URL: "https://pay.example.com/" + c.Reference, ProviderOrderID: strconv.Itoa(base + len(g.pages))}, nil
}

func (g *checkoutGateway) ParseCallback(q url.Values, body []byte) (app.Callback, error) {
	if q.Get("hmac") != "ok" {
		return app.Callback{}, app.ErrForgedCallback
	}
	return g.ReadCallback(body)
}

func (*checkoutGateway) ReadCallback(body []byte) (app.Callback, error) {
	f := strings.Fields(string(body))
	if len(f) != 4 {
		return app.Callback{}, app.ErrBadCallback
	}
	cb := app.Callback{EventID: f[0] + ":" + f[1] + f[2], ProviderOrderID: f[2]}
	if f[0] == "token" {
		cb.Kind, cb.Token, cb.Label = app.CallbackToken, f[3], "Visa •••• 4242"
	} else {
		cb.Kind, cb.Reference, cb.TransactionID = app.CallbackTransaction, f[1], "txn-"+f[1]
	}
	return cb, nil
}

func (g *checkoutGateway) Paid(_ context.Context, _, reference string, _ int64) (string, bool, error) {
	return "txn-" + reference, g.paid[reference], nil
}

// TestPayNowSavesTheCardAndRestores: a suspended store's owner pays on
// the gateway's page; the token callback saves the card, the
// transaction callback (checked by asking the gateway) pays the invoice
// and restores the store (ADR-0053).
func TestPayNowSavesTheCardAndRestores(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	gw := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}}
	inbox := webhookstest.NewInbox()
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": gw}, Sealer: plainSealer{}, Holds: d.holds.set, Inbox: inbox,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com"})
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	if err := d.svc.InvoiceStore(ctx, october); err != nil {
		t.Fatal(err)
	}
	js := d.accountJobs()
	if err := d.svc.InvoiceAccount(t.Context(), js[0].AccountID, js[0].Period); err != nil {
		t.Fatal(err)
	}
	d.jobs.Args = nil
	account := js[0].AccountID
	// The renewal went unpaid through its grace: suspended and held.
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'suspended', suspended_at = '2026-11-08T00:00:00Z'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d.holds.on[tenant.StoreID{ID: storeOf(ctx)}] = true
	d.own(ctx, account)
	invoice := d.invoiceID(t, account)

	sum, err := d.svc.Summary(ctx)
	if err != nil || sum.Status != "suspended" || sum.Card != "" || len(sum.OpenInvoices) != 1 || sum.PlanNameEn != "Pro" {
		t.Fatalf("summary before = %+v, %v", sum, err)
	}
	link, err := d.svc.PayNow(ctx, invoice, app.Payer{})
	if err != nil || !strings.HasPrefix(link, "https://pay.example.com/bc_") {
		t.Fatalf("pay now = %q, %v", link, err)
	}
	page := gw.pages[0]
	id, _ := tenant.FromContext(ctx)
	if page.NotificationURL != "https://api.example.com/v1/webhooks/paymob_billing" || page.RedirectURL != "https://app.example.com/stores/"+id.String()+"/billing" ||
		page.AmountMinor != 59900+8386 || !strings.HasPrefix(page.Description, "INV-") {
		t.Fatalf("page = %+v", page)
	}
	ref := page.Reference
	order := "4547"

	// Callbacks reach the platform URL and are placed in the store by our
	// reference (the payment) or Paymob's order (the card).
	provider, ok := d.svc.CallbackProvider()
	if !ok || provider.Name() != "paymob_billing" {
		t.Fatal("no callback provider")
	}
	signed := url.Values{"hmac": {"ok"}}
	tokenBody := []byte("token - " + order + " tok-1")
	parts, err := provider.VerifyPlatform(t.Context(), webhooks.Request{Query: signed, Body: tokenBody})
	if err != nil || len(parts) != 1 || parts[0].Store != id {
		t.Fatalf("token placed = %+v, %v", parts, err)
	}
	txnBody := []byte("transaction " + ref + " " + order + " -")
	if parts, err = provider.VerifyPlatform(t.Context(), webhooks.Request{Query: signed, Body: txnBody}); err != nil || len(parts) != 1 || parts[0].Store != id {
		t.Fatalf("transaction placed = %+v, %v", parts, err)
	}
	if _, err = provider.VerifyPlatform(t.Context(), webhooks.Request{Body: txnBody}); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("unsigned: %v", err)
	}
	if parts, err = provider.VerifyPlatform(t.Context(), webhooks.Request{Query: signed, Body: []byte("transaction bc_NOPE 999 -")}); err != nil || len(parts) != 0 {
		t.Fatalf("a callback of no checkout = %+v, %v", parts, err)
	}

	// The card is saved.
	tokenReceipt := inbox.Put("paymob_billing", tokenBody)
	if err = d.svc.ProcessCallback(ctx, tokenReceipt); err != nil {
		t.Fatal(err)
	}
	if inbox.Outcome(tokenReceipt) != "card_saved" {
		t.Fatalf("token outcome = %q", inbox.Outcome(tokenReceipt))
	}
	if sum, _ = d.svc.Summary(ctx); sum.Card != "Visa •••• 4242" {
		t.Fatalf("card = %q", sum.Card)
	}
	// A payment the gateway does not confirm settles nothing.
	txnReceipt := inbox.Put("paymob_billing", txnBody)
	if err = d.svc.ProcessCallback(ctx, txnReceipt); err != nil || inbox.Outcome(txnReceipt) != "not_paid" {
		t.Fatalf("unconfirmed: %q, %v", inbox.Outcome(txnReceipt), err)
	}
	if s, _, _ := d.invoice(t, account); s != "open" {
		t.Fatalf("an unconfirmed payment paid the invoice: %s", s)
	}
	// Confirmed: paid, restored, the card kept for the next invoice.
	gw.paid[ref] = true
	again := inbox.Put("paymob_billing", txnBody)
	if err = d.svc.ProcessCallback(ctx, again); err != nil || inbox.Outcome(again) != "paid" {
		t.Fatalf("confirmed: %q, %v", inbox.Outcome(again), err)
	}
	if n := d.run(t); len(n) != 1 {
		t.Fatalf("notices = %v", n)
	}
	if s, _, _ := d.invoice(t, account); s != "paid" || d.holds.of(ctx) {
		t.Fatalf("after payment: invoice %s, held %v", s, d.holds.of(ctx))
	}
	if st, _, _, _ := d.state(ctx, t); st != "active" {
		t.Fatalf("store = %s", st)
	}
	// Paying again: the invoice is not open.
	if _, err = d.svc.PayNow(ctx, invoice, app.Payer{}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("pay a paid invoice: %v", err)
	}
	if bad := inbox.Put("paymob_billing", []byte("x")); d.svc.ProcessCallback(ctx, bad) != nil || inbox.Outcome(bad) != "unreadable" {
		t.Fatalf("unreadable outcome = %q", inbox.Outcome(bad))
	}
}

// TestPayNowRefusals: another store's invoice is not found, a store
// never invoiced has none, and a currency without a checkout is
// unavailable.
func TestPayNowRefusals(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	gw := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}}
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Now: func() time.Time { return *d.clock },
		Stores:   func(context.Context) (app.StoreInfo, error) { return app.StoreInfo{Name: "s", Currency: "EGP"}, nil },
		Gateways: map[string]app.Gateway{"paymob": gw}, PublicURL: "https://api.example.com"})
	a, b := d.store(t), d.store(t)
	d.subscribe(a, t, "pays", "active", "")
	d.fee(a, t, october, 2500, "due")
	if err := d.svc.InvoiceStore(a, october); err != nil {
		t.Fatal(err)
	}
	js := d.accountJobs()
	if err := d.svc.InvoiceAccount(t.Context(), js[0].AccountID, js[0].Period); err != nil {
		t.Fatal(err)
	}
	invoice := d.invoiceID(t, js[0].AccountID)
	d.subscribe(b, t, "pays", "active", "")
	for name, ctx := range map[string]context.Context{"store B": b, "a store never invoiced": d.store(t)} {
		if _, err := d.svc.PayNow(ctx, invoice, app.Payer{}); !errs.Is(err, errs.KindNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := app.NewService(app.Deps{DB: d.pool}).PayNow(a, invoice, app.Payer{}); !errs.Is(err, errs.KindUnavailable) {
		t.Errorf("no checkout gateway: %v", err)
	}
	if _, ok := app.NewService(app.Deps{DB: d.pool}).CallbackProvider(); ok {
		t.Error("a callback provider without a checkout gateway")
	}
	if err := app.NewService(app.Deps{DB: d.pool}).ProcessCallback(a, d.ids.New()); err == nil {
		t.Error("ProcessCallback without a gateway and inbox")
	}
	if app.CallbackURL("https://api.example.com/") != "https://api.example.com/v1/webhooks/paymob_billing" {
		t.Error("callback URL")
	}
}

// TestCallbacksOfNothing: a receipt that expired, a callback whose
// checkout is not in this store, and a free store's summary.
func TestCallbacksOfNothing(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	gw := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}}
	inbox := webhookstest.NewInbox()
	svc := app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Gateways: map[string]app.Gateway{"paymob": gw}, Inbox: inbox})
	ctx := d.store(t)
	if err := (&app.ProcessCallbackWorker{Svc: svc}).Work(ctx, &river.Job[app.ProcessCallbackArgs]{Args: app.ProcessCallbackArgs{ReceiptID: d.ids.New()}}); err != nil {
		t.Fatalf("an expired receipt: %v", err)
	}
	r := inbox.Put("paymob_billing", []byte("transaction bc_01NOTHERE0000000000000000000 4547 -"))
	if err := svc.ProcessCallback(ctx, r); err != nil || inbox.Outcome(r) != "unplaced" {
		t.Fatalf("no checkout: %q, %v", inbox.Outcome(r), err)
	}
	sum, err := svc.Summary(ctx)
	if err != nil || sum.PlanNameEn != "Free" || sum.Status != "active" || sum.Card != "" || len(sum.OpenInvoices) != 0 {
		t.Fatalf("free store = %+v, %v", sum, err)
	}
	if (app.ProcessCallbackArgs{}).Kind() != "billing.process_callback" || app.BillingConnection().IsZero() {
		t.Error("kind or connection")
	}
	p, _ := svc.CallbackProvider()
	if _, err = p.ChallengePlatform(t.Context(), nil); !errors.Is(err, webhooks.ErrForged) {
		t.Errorf("challenge: %v", err)
	}
	if parts, vErr := p.VerifyPlatform(t.Context(), webhooks.Request{Query: url.Values{"hmac": {"ok"}}, Body: []byte("x")}); vErr != nil || len(parts) != 0 {
		t.Errorf("unreadable signed callback = %v, %v", parts, vErr)
	}
}

// TestASecondPaymentIsKeptForRefund: the owner pays one invoice on two
// gateway pages. The second payment reaches an invoice already paid: it
// is recorded as a duplicate for the console to refund by hand (the
// owner's decision, 2026-10-31), never dropped.
func TestASecondPaymentIsKeptForRefund(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	gw := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}, base: 91000}
	inbox := webhookstest.NewInbox()
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": gw}, Sealer: plainSealer{}, Holds: d.holds.set, Inbox: inbox,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com"})
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	if err := d.svc.InvoiceStore(ctx, october); err != nil {
		t.Fatal(err)
	}
	js := d.accountJobs()
	if err := d.svc.InvoiceAccount(t.Context(), js[0].AccountID, js[0].Period); err != nil {
		t.Fatal(err)
	}
	account := js[0].AccountID
	d.own(ctx, account)
	invoice := d.invoiceID(t, account)

	for range 2 {
		if _, err := d.svc.PayNow(ctx, invoice, app.Payer{}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := gw.pages[0], gw.pages[1]
	gw.paid[first.Reference], gw.paid[second.Reference] = true, true
	pay := func(p app.CheckoutRequest, order string) string {
		r := inbox.Put("paymob_billing", []byte("transaction "+p.Reference+" "+order+" -"))
		if err := d.svc.ProcessCallback(ctx, r); err != nil {
			t.Fatal(err)
		}
		return inbox.Outcome(r)
	}
	if o := pay(first, "91001"); o != "paid" {
		t.Fatalf("first payment = %q", o)
	}
	if o := pay(second, "91002"); o != "duplicate" {
		t.Fatalf("second payment = %q, want duplicate", o)
	}
	if o := pay(second, "91002"); o != "duplicate" {
		t.Fatalf("its callback again = %q", o)
	}

	// The console lists it once, with what the admin needs to refund it.
	dups, err := d.svc.DuplicatePayments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var mine []app.DuplicatePayment
	for _, p := range dups {
		if p.Store == storeOf(ctx) {
			mine = append(mine, p)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("duplicates = %+v", mine)
	}
	p := mine[0]
	if p.Gateway != "paymob" || p.Reference != "txn-"+second.Reference || p.AmountMinor != second.AmountMinor || p.Currency != "EGP" ||
		!strings.HasPrefix(p.InvoiceNumber, "INV-") || p.RefundedAt != nil {
		t.Fatalf("duplicate = %+v", p)
	}

	if err = d.svc.MarkRefunded(t.Context(), p.ID, " "); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reference: %v", err)
	}
	if err = d.svc.MarkRefunded(t.Context(), p.ID, "PMB-REF-77"); err != nil {
		t.Fatal(err)
	}
	if err = d.svc.MarkRefunded(t.Context(), p.ID, "PMB-REF-77"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("refunded twice: %v", err)
	}
	if err = d.svc.MarkRefunded(t.Context(), d.ids.New(), "x"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown payment: %v", err)
	}
	if dups, err = d.svc.DuplicatePayments(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, q := range dups {
		if q.ID == p.ID && (q.RefundedAt == nil || q.RefundReference != "PMB-REF-77") {
			t.Fatalf("after refund = %+v", q)
		}
	}
	// The invoice stays paid once; a transfer of it now is refused, and
	// its card on file is not charged.
	if s, _, _ := d.invoice(t, account); s != "paid" {
		t.Fatalf("invoice = %s", s)
	}
	if err = d.svc.SettleInvoice(t.Context(), account, invoice, "TR-9"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("settle a paid invoice: %v", err)
	}
	if err = d.svc.ChargeInvoice(t.Context(), account, invoice); err != nil {
		t.Fatalf("charge a paid invoice: %v", err)
	}
}
