package app_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestSettleStoreInvoice: the console settles an open invoice of the
// store's account like any payment, and only such an invoice (ADR-0058).
func TestSettleStoreInvoice(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx, other, bare := d.store(t), d.store(t), d.store(t)
	d.own(ctx, ulid.ID{})
	period := "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'"
	d.subscribe(ctx, t, "pro", "active", period)
	d.subscribe(other, t, "pro", "active", period)
	account, otherAccount := d.invoiceOf(ctx, t), d.invoiceOf(other, t)
	invoice := d.invoiceID(t, account)

	h, err := d.svc.Health(t.Context())
	if err != nil || h.InvoicesOpen < 2 || h.Active < 2 {
		t.Fatalf("health = %+v, %v", h, err)
	}

	for name, err := range map[string]error{
		"another account's invoice":  d.svc.SettleStoreInvoice(ctx, d.invoiceID(t, otherAccount), "transfer-1"),
		"a store without an account": d.svc.SettleStoreInvoice(bare, invoice, "transfer-1"),
		"an unknown invoice":         d.svc.SettleStoreInvoice(ctx, d.ids.New(), "transfer-1"),
	} {
		if !errs.Is(err, errs.KindNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st, _, _ := d.invoice(t, otherAccount); st != "open" {
		t.Fatalf("the other account's invoice = %s", st)
	}

	if err = d.svc.SettleStoreInvoice(ctx, invoice, "transfer-1"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := d.invoice(t, account); st != "paid" {
		t.Fatalf("after settling: %s", st)
	}
	if _, _, periodEnd, billed := d.state(ctx, t); billed || periodEnd.Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("renewal not applied: period end %v, billed %v", periodEnd, billed)
	}
	// Paid, it is no longer open: 404.
	if err = d.svc.SettleStoreInvoice(ctx, invoice, "transfer-2"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("settle twice: %v", err)
	}
}
