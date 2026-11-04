package app_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
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

// TestConsoleInvoicesAndPayments: the console's lists of every account's
// invoices and payment attempts, an invoice with its lines and attempts,
// and the summary's counts (VAT included in what was collected).
func TestConsoleInvoicesAndPayments(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if err := d.svc.SavePaymentMethod(t.Context(), account, "fake", "decline-card", "Visa •••• 2346"); err != nil {
		t.Fatal(err)
	}
	d.tick(t, runAt.Add(time.Hour))          // declined
	*d.clock = runAt.Add(4 * 24 * time.Hour) // past its due day
	invoice := d.invoiceID(t, account)

	got, err := d.svc.Invoice(t.Context(), invoice)
	if err != nil {
		t.Fatal(err)
	}
	inv := got.Invoice
	if inv.Account != account || inv.AccountKind != "store" || inv.Store == nil || inv.Status != "open" || !inv.Overdue ||
		inv.TotalMinor != 59900+8386 || inv.TaxMinor != 8386 || inv.TaxBps != 1400 || inv.Attempts != 1 || inv.NextAttemptAt == nil ||
		inv.LastError != "insufficient funds" || inv.PaidAt != nil {
		t.Fatalf("invoice = %+v", inv)
	}
	if len(got.Lines) != 1 || got.Lines[0].Kind != "subscription" || got.Lines[0].AmountMinor != 59900 {
		t.Fatalf("lines = %+v", got.Lines)
	}
	if p := got.Payments; len(p) != 1 || p[0].Outcome != "declined" || p[0].Reason != "insufficient funds" || p[0].Gateway != "fake" ||
		p[0].AmountMinor != inv.TotalMinor || p[0].Currency != "EGP" || p[0].InvoiceNumber != inv.Number {
		t.Fatalf("payments = %+v", p)
	}
	if _, err = d.svc.Invoice(t.Context(), d.ids.New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown invoice: %v", err)
	}

	// The filters narrow to it, or away from it.
	list := func(q app.InvoiceQuery) []app.ConsoleInvoice {
		t.Helper()
		p, listErr := d.svc.Invoices(t.Context(), q, "", 50)
		if listErr != nil {
			t.Fatalf("%+v: %v", q, listErr)
		}
		return p.Items
	}
	name := inv.AccountName
	for q, want := range map[app.InvoiceQuery]int{
		{Q: name}:                           1,
		{Q: inv.Number}:                     1,
		{Q: name, Status: "open"}:           1,
		{Q: name, Status: "overdue"}:        1,
		{Q: name, Currency: "EGP"}:          1,
		{Q: name, Status: "paid"}:           0,
		{Q: name, Currency: "SAR"}:          0,
		{Q: inv.Number[len(inv.Number)-3:]}: 0, // a number matches from its start
		{Q: "%"}:                            0, // literal, not a wildcard
	} {
		if items := list(q); len(items) != want || want == 1 && items[0].ID != invoice {
			t.Errorf("%+v: %d items; want %d", q, len(items), want)
		}
	}
	for name, q := range map[string]app.InvoiceQuery{
		"status": {Status: "late"}, "currency": {Currency: "USD"}, "q": {Q: strings.Repeat("x", 101)},
	} {
		if _, err = d.svc.Invoices(t.Context(), q, "", 10); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"cursor":  func() error { _, e := d.svc.Invoices(t.Context(), app.InvoiceQuery{}, "junk", 10); return e }(),
		"limit":   func() error { _, e := d.svc.Invoices(t.Context(), app.InvoiceQuery{}, "", app.MaxListPage+1); return e }(),
		"outcome": func() error { _, e := d.svc.Payments(t.Context(), app.PaymentQuery{Outcome: "lost"}, "", 10); return e }(),
		"gateway": func() error {
			_, e := d.svc.Payments(t.Context(), app.PaymentQuery{Gateway: "Pay Mob"}, "", 10)
			return e
		}(),
		"pcursor": func() error { _, e := d.svc.Payments(t.Context(), app.PaymentQuery{}, "junk", 10); return e }(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Settled by transfer: paid, collected this month with its VAT.
	if err = d.svc.SettleInvoice(t.Context(), account, invoice, "TRX-77"); err != nil {
		t.Fatal(err)
	}
	if items := list(app.InvoiceQuery{Q: name, Status: "paid"}); len(items) != 1 || items[0].PaidAt == nil || items[0].Overdue {
		t.Fatalf("paid = %+v", items)
	}
	p, err := d.svc.Payments(t.Context(), app.PaymentQuery{Outcome: "manual", Gateway: "manual"}, "", app.MaxListPage)
	if err != nil || !slices.ContainsFunc(p.Items, func(a app.ConsolePayment) bool { return a.Invoice == invoice && a.Reference == "TRX-77" }) {
		t.Fatalf("manual payments = %d, %v", len(p.Items), err)
	}
	// The search finds a payment by its exact reference only (ADR-0079).
	ref := "TRX-77-" + invoice.String()
	if err = d.svc.SettleInvoice(t.Context(), account, d.ids.New(), ref); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("settle an unknown invoice: %v", err)
	}
	byRef, err := d.svc.PaymentsByReference(t.Context(), "TRX-77")
	if err != nil || !slices.ContainsFunc(byRef, func(a app.ConsolePayment) bool { return a.Invoice == invoice && a.Outcome == "manual" }) {
		t.Fatalf("by reference = %+v, %v", byRef, err)
	}
	for _, q := range []string{"TRX", " ", strings.Repeat("x", 121)} {
		if got, refErr := d.svc.PaymentsByReference(t.Context(), q); refErr != nil || slices.ContainsFunc(got, func(a app.ConsolePayment) bool { return a.Invoice == invoice }) {
			t.Errorf("%q = %+v, %v", q, got, refErr)
		}
	}
	tot, err := d.svc.Totals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !tot.MonthStart.Equal(time.Date(2026, 10, 31, 22, 0, 0, 0, time.UTC)) { // 1 November in Cairo (UTC+2)
		t.Fatalf("month start = %v", tot.MonthStart)
	}
	egp := func(list []app.CurrencyTotal) app.CurrencyTotal {
		for _, c := range list {
			if c.Currency == "EGP" {
				return c
			}
		}
		return app.CurrencyTotal{}
	}
	if c := egp(tot.Collected); c.Count < 1 || c.TotalMinor < inv.TotalMinor || c.TaxMinor < inv.TaxMinor {
		t.Fatalf("collected = %+v", tot.Collected)
	}
	if !slices.ContainsFunc(tot.Failed, func(f app.FailedCount) bool { return f.Outcome == "declined" && f.Reason == "insufficient funds" }) {
		t.Fatalf("failed = %+v", tot.Failed)
	}
}

// TestConsolePages: a page's cursor carries on after its last row, with
// no row twice, for invoices and payments alike.
func TestConsolePages(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	for range 3 {
		ctx := d.store(t)
		d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
		account := d.invoiceOf(ctx, t)
		if err := d.svc.SettleInvoice(t.Context(), account, d.invoiceID(t, account), "TRX-1"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := d.svc.Invoices(t.Context(), app.InvoiceQuery{}, "", 2)
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := d.svc.Invoices(t.Context(), app.InvoiceQuery{}, first.NextCursor, 2)
	if err != nil || len(second.Items) == 0 {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	last, next := first.Items[1], second.Items[0]
	if next.ID == last.ID || next.IssuedAt.After(last.IssuedAt) {
		t.Fatalf("second page starts at %v %s after %v %s", next.IssuedAt, next.ID, last.IssuedAt, last.ID)
	}
	pf, err := d.svc.Payments(t.Context(), app.PaymentQuery{}, "", 2)
	if err != nil || len(pf.Items) != 2 || pf.NextCursor == "" {
		t.Fatalf("first payments = %+v, %v", pf, err)
	}
	ps, err := d.svc.Payments(t.Context(), app.PaymentQuery{}, pf.NextCursor, 2)
	if err != nil || len(ps.Items) == 0 || ps.Items[0].ID == pf.Items[1].ID || ps.Items[0].CreatedAt.After(pf.Items[1].CreatedAt) {
		t.Fatalf("second payments = %+v, %v", ps, err)
	}
}
