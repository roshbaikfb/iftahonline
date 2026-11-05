package app_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestCardThreshold: an account that pays by card is invoiced as soon
// as its due fees reach the minimum (300), the dunning charges it, the
// fees below the minimum wait for the month's invoice, and no fee is
// on two invoices (ADR-0086).
func TestCardThreshold(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	credit, _ := domain.ParseCredit(100, "correction", "open the account")
	if _, err := p.svc.CreditBalance(ctx, credit, p.ids.New()); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)
	if err := postgres.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE billing_accounts SET collection = 'card'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Visa •••• 4242"); err != nil {
		t.Fatal(err)
	}
	base := 20000 * 10000 / p.rate(ctx, t) // a fee of 200
	day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	deliver := func(n int) {
		for i := range n {
			p.move(ctx, t, p.ids.New(), app.OrderDelivered, base, day.Add(time.Duration(i)*time.Minute))
		}
	}

	// 200 due: under the minimum, nothing yet.
	deliver(1)
	if invs := p.invoices(t, account); len(invs) != 0 {
		t.Fatalf("under the minimum: %+v", invs)
	}
	// 400 due: invoiced at once, with its tax (14%), and charged within the hour.
	deliver(1)
	invs := p.invoices(t, account)
	if len(invs) != 1 || invs[0].subtotal != 40000 || invs[0].tax != 5600 || invs[0].status != "open" {
		t.Fatalf("at the minimum: %+v", invs)
	}
	if p.tick(t, runAt.Add(time.Hour)); len(p.gw.charges) != 1 || p.gw.charges[0].AmountMinor != 45600 {
		t.Fatalf("charges = %+v", p.gw.charges)
	}
	if invs = p.invoices(t, account); invs[0].status != "paid" {
		t.Fatalf("after the charge: %+v", invs)
	}
	// The balance is not touched: the card pays.
	if bal, _ := p.svc.StoreBalance(ctx); bal.Minor != 100 {
		t.Fatalf("balance = %d", bal.Minor)
	}

	// 200 more waits for the month's invoice, which bills only it.
	deliver(1)
	if invs = p.invoices(t, account); len(invs) != 1 {
		t.Fatalf("a second fees invoice under the minimum: %+v", invs)
	}
	if err := p.svc.InvoiceAccount(t.Context(), account, october); err != nil {
		t.Fatal(err)
	}
	invs = p.invoices(t, account)
	var billed int64
	for _, inv := range invs {
		billed += inv.subtotal
	}
	if len(invs) != 2 || billed != 60000 {
		t.Fatalf("every fee billed once: %d over %+v", billed, invs)
	}
	for id, f := range p.fees(ctx, t) {
		if f.status != "invoiced" {
			t.Errorf("fee %s: %s", id, f.status)
		}
	}
}
