package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestTopUp: a balance is topped up on the gateway's page (ADR-0085):
// refused while the balance is neither offered nor chosen, and below the
// minimum; added once the gateway confirms the payment, once only.
func TestTopUp(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	gw := &checkoutGateway{fakeGateway: p.gw, paid: map[string]bool{}, base: 7100}
	inbox := webhookstest.NewInbox()
	p.svc = app.NewService(app.Deps{DB: p.pool, Jobs: p.jobs, IDs: p.ids, Events: p.events, Now: func() time.Time { return *p.clock },
		Orders: p.book.get,
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": gw}, Sealer: plainSealer{}, Holds: p.holds.set, Inbox: inbox,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com"})
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	credit, _ := domain.ParseCredit(10000, "transfer", "CIB 3")
	if _, err := p.svc.CreditBalance(ctx, credit, p.ids.New()); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)

	// Neither offered by the platform nor chosen by the account.
	if _, err := p.svc.TopUp(ctx, 30000, app.Payer{}); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("not offered: %v", err)
	}
	p.choose(ctx, t, account)
	if _, err := p.svc.TopUp(ctx, 29999, app.Payer{}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("under the minimum: %v", err)
	}
	link, err := p.svc.TopUp(ctx, 30000, app.Payer{Name: "سارة", Email: "sara@example.com"})
	if err != nil || len(gw.pages) != 1 {
		t.Fatalf("top up = %q, %v", link, err)
	}
	page := gw.pages[0]
	id := ulid.ID(storeOf(ctx))
	if link != "https://pay.example.com/"+page.Reference || page.AmountMinor != 30000 || page.Currency != "EGP" || page.Description != "شحن الرصيد" ||
		page.RedirectURL != "https://app.example.com/stores/"+id.String()+"/billing/balance" {
		t.Fatalf("page = %+v", page)
	}

	// The callback is a hint: an unconfirmed payment adds nothing.
	body := []byte("transaction " + page.Reference + " 7101 -")
	first := inbox.Put("paymob_billing", body)
	if err = p.svc.ProcessCallback(ctx, first); err != nil || inbox.Outcome(first) != "not_paid" {
		t.Fatalf("unconfirmed: %q, %v", inbox.Outcome(first), err)
	}
	if bal, _ := p.svc.StoreBalance(ctx); bal.Minor != 10000 {
		t.Fatalf("an unconfirmed top-up added: %d", bal.Minor)
	}
	// Confirmed: added once, told; a repeated callback adds nothing.
	gw.paid[page.Reference] = true
	p.jobs.Args = nil
	for i, want := range []string{"topped_up", "duplicate"} {
		r := inbox.Put("paymob_billing", body)
		if err = p.svc.ProcessCallback(ctx, r); err != nil || inbox.Outcome(r) != want {
			t.Fatalf("callback %d: %q, %v", i, inbox.Outcome(r), err)
		}
	}
	bal, err := p.svc.StoreBalance(ctx)
	if err != nil || bal.Minor != 40000 || bal.Entries[0].Kind != domain.EntryTopUp || bal.Entries[0].Reference != "txn-"+page.Reference ||
		bal.Entries[0].Checkout.IsZero() || len(bal.Entries) != 2 {
		t.Fatalf("after the top-up = %+v, %v", bal, err)
	}
	if n := p.notices(); !sameKinds(n, domain.NoticeToppedUp) {
		t.Fatalf("notices = %v", n)
	}
}
