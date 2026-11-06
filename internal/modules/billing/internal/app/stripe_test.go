package app_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// stripeGateway is a fake Stripe: a page per checkout with a session, a
// callback body "<reference>" signed with the header Stripe-Signature:
// ok, and the card saved with the payment's answer.
type stripeGateway struct {
	*fakeGateway
	pages   []app.CheckoutRequest
	paid    map[string]bool
	queries []app.PaidQuery
}

var _ app.CheckoutGateway = (*stripeGateway)(nil)

func (g *stripeGateway) Checkout(_ context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	g.pages = append(g.pages, c)
	return app.CheckoutPage{URL: "https://checkout.stripe.com/" + c.Reference, Session: "cs_" + c.Reference}, nil
}

func (g *stripeGateway) ParseCallback(h http.Header, _ url.Values, body []byte) (app.Callback, error) {
	if h.Get("Stripe-Signature") != "ok" {
		return app.Callback{}, app.ErrForgedCallback
	}
	return g.ReadCallback(body)
}

func (*stripeGateway) ReadCallback(body []byte) (app.Callback, error) {
	ref := string(body)
	if !strings.HasPrefix(ref, "bc_") {
		return app.Callback{}, app.ErrBadCallback
	}
	return app.Callback{Kind: app.CallbackTransaction, EventID: "evt_" + ref, Reference: ref}, nil
}

func (g *stripeGateway) Paid(_ context.Context, q app.PaidQuery) (app.Payment, error) {
	g.queries = append(g.queries, q)
	if !g.paid[q.Reference] {
		return app.Payment{TransactionID: "pi_" + q.Reference}, nil
	}
	return app.Payment{TransactionID: "pi_" + q.Reference, Paid: true, Token: "cus_1 pm_1", Label: "VISA •••• 4242"}, nil
}

// TestStripeCheckout: a Saudi merchant, billed in dollars, pays its
// invoice on Stripe's page, its country's billing gateway (ADR-0091):
// Stripe's callback is placed by our reference, Stripe is asked by the
// session, and the card it saved becomes the account's card on file,
// charged by Stripe next time. A callback of the other gateway about it
// is not acted on; without Stripe the page is unavailable.
func TestStripeCheckout(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	pm := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}}
	st := &stripeGateway{fakeGateway: &fakeGateway{}, paid: map[string]bool{}}
	inbox := webhookstest.NewInbox()
	deps := app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر الرياض", Currency: "SAR", Country: "SA"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": pm, "stripe": st}, Sealer: plainSealer{}, Holds: d.holds.set, Inbox: inbox,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com"}
	d.svc = app.NewService(deps)
	ctx := d.gulfStore(t)
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

	link, err := d.svc.PayNow(ctx, invoice, app.Payer{Email: "owner@example.com"})
	if err != nil || !strings.HasPrefix(link, "https://checkout.stripe.com/bc_") || len(pm.pages) != 0 || len(st.pages) != 1 {
		t.Fatalf("pay now = %q, %v (paymob %d, stripe %d pages)", link, err, len(pm.pages), len(st.pages))
	}
	page := st.pages[0]
	if page.Currency != "USD" || page.NotificationURL != "https://api.example.com/v1/webhooks/stripe_billing" || page.Email != "owner@example.com" {
		t.Fatalf("page = %+v", page)
	}
	ref := page.Reference

	providers := d.svc.CallbackProviders()
	if len(providers) != 2 || providers[0].Name() != "paymob_billing" || providers[1].Name() != "stripe_billing" {
		t.Fatalf("providers = %v", providers)
	}
	stripeHook := providers[1]
	id, _ := tenant.FromContext(ctx)
	parts, err := stripeHook.VerifyPlatform(t.Context(), webhooks.Request{Header: http.Header{"Stripe-Signature": {"ok"}}, Body: []byte(ref)})
	if err != nil || len(parts) != 1 || parts[0].Store != id || parts[0].EventID != "evt_"+ref {
		t.Fatalf("placed = %+v, %v", parts, err)
	}
	if _, err = stripeHook.VerifyPlatform(t.Context(), webhooks.Request{Body: []byte(ref)}); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("unsigned: %v", err)
	}

	// Paymob's callback naming this checkout is not acted on.
	wrong := inbox.Put("paymob_billing", []byte("transaction "+ref+" 99999 -"))
	if err = d.svc.ProcessCallback(ctx, wrong); err != nil || inbox.Outcome(wrong) != "other_gateway" {
		t.Fatalf("the other gateway's callback: %q, %v", inbox.Outcome(wrong), err)
	}
	// Not paid yet: nothing settles, no card.
	early := inbox.Put("stripe_billing", []byte(ref))
	if err = d.svc.ProcessCallback(ctx, early); err != nil || inbox.Outcome(early) != "not_paid" {
		t.Fatalf("unpaid: %q, %v", inbox.Outcome(early), err)
	}
	if q := st.queries[0]; q.Session != "cs_"+ref || q.Reference != ref || q.Currency != "USD" || q.AmountMinor != page.AmountMinor {
		t.Fatalf("asked = %+v", q)
	}
	st.paid[ref] = true
	paid := inbox.Put("stripe_billing", []byte(ref))
	if err = d.svc.ProcessCallback(ctx, paid); err != nil || inbox.Outcome(paid) != "paid" {
		t.Fatalf("paid: %q, %v", inbox.Outcome(paid), err)
	}
	if s, _, _ := d.invoice(t, account); s != "paid" {
		t.Fatalf("invoice = %s", s)
	}
	sum, err := d.svc.Summary(ctx)
	// A Saudi merchant pays on Stripe: the page says so (ADR-0089).
	if err != nil || sum.Card != "VISA •••• 4242" || len(sum.OpenInvoices) != 0 || sum.Gateway != "stripe" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	var gateway string
	if err = postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT gateway FROM billing_checkouts WHERE reference = $1`, ref).Scan(&gateway)
	}); err != nil || gateway != "stripe" {
		t.Fatalf("checkout gateway = %q, %v", gateway, err)
	}

	// Without the gateway of its country, a merchant has no page.
	deps.Gateways = map[string]app.Gateway{"paymob": pm}
	noStripe := app.NewService(deps)
	other := d.gulfStore(t)
	d.subscribe(other, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	d.jobs.Args = nil
	if err = noStripe.InvoiceStore(other, october); err != nil {
		t.Fatal(err)
	}
	js = d.accountJobs()
	if err = noStripe.InvoiceAccount(t.Context(), js[0].AccountID, js[0].Period); err != nil {
		t.Fatal(err)
	}
	d.own(other, js[0].AccountID)
	if _, err = noStripe.PayNow(other, d.invoiceID(t, js[0].AccountID), app.Payer{}); !errs.Is(err, errs.KindUnavailable) || len(pm.pages) != 0 {
		t.Fatalf("no Stripe: %v (%d Paymob pages)", err, len(pm.pages))
	}
	if err = noStripe.ProcessCallback(ctx, inbox.Put("stripe_billing", []byte(ref))); err == nil {
		t.Fatal("a Stripe receipt processed without Stripe")
	}
}
