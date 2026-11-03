package app_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// seedInvoice opens an account (a store's when shop is set, else an
// agency named name) with one open invoice past due, and returns the
// invoice and its number.
func (f fixture) seedInvoice(t *testing.T, shop *tenant.StoreID, name string) (ulid.ID, string) {
	t.Helper()
	account, invoice := f.ids.New(), f.ids.New()
	number := fmt.Sprintf("INV-2099-%012d", rand.Int64N(1e12)) //nolint:gosec // a test number
	kind, plan := "agency", any("agency_pro")
	if shop != nil {
		kind, plan = "store", nil
	}
	issued := time.Now().Add(-10 * 24 * time.Hour)
	err := postgres.InTx(t.Context(), f.owner, func(tx pgx.Tx) error {
		steps := []struct {
			sql  string
			args []any
		}{
			{`SELECT set_config('app.account_id', $1::uuid::text, true)`, []any{account}},
			{`INSERT INTO billing_accounts (id, kind, name, currency, country, plan_code) VALUES ($1, $2, $3, 'EGP', 'EG', $4)`,
				[]any{account, kind, name, plan}},
			{`INSERT INTO invoices (id, account_id, number, period, currency, subtotal_minor, tax_bps, tax_minor, total_minor, issued_at, due_at)
			  VALUES ($1, $2, $3, '2099-01-01', 'EGP', 10000, 1400, 1400, 11400, $4, $5)`, []any{invoice, account, number, issued, issued.Add(72 * time.Hour)}},
			{`INSERT INTO invoice_lines (invoice_id, account_id, line_no, kind, description, quantity, amount_minor)
			  VALUES ($1, $2, 1, 'subscription', 'Pro (monthly)', 1, 10000)`, []any{invoice, account}},
		}
		if shop != nil {
			steps = append(steps, struct {
				sql  string
				args []any
			}{`INSERT INTO billing_account_stores (account_id, member_store_id) VALUES ($1, $2)`, []any{account, shop.ID}})
		}
		for _, s := range steps {
			if _, err := tx.Exec(t.Context(), s.sql, s.args...); err != nil {
				return fmt.Errorf("%s: %w", s.sql, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return invoice, number
}

// TestConsoleInvoices: every account's invoices from the console, a
// store's under the store's name now and an agency's under its own, and
// an agency's invoice settled by transfer, recorded first in the admin
// log without naming a store.
func TestConsoleInvoices(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	shop := f.createStore(t)
	storeInvoice, storeNumber := f.seedInvoice(t, &shop, "Old name")
	agencyInvoice, agencyNumber := f.seedInvoice(t, nil, "=HYPERLINK(\"x\") Agency")

	page, err := f.svc.Invoices(ctx, billing.InvoiceQuery{Q: storeNumber}, "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("store's invoice = %+v, %v", page, err)
	}
	if inv := page.Items[0]; inv.ID != storeInvoice || inv.AccountName != "Victim" || inv.Store == nil || *inv.Store != shop.ID || !inv.Overdue {
		t.Fatalf("store's invoice = %+v", inv)
	}
	d, err := f.svc.Invoice(ctx, agencyInvoice)
	if err != nil || d.Invoice.AccountKind != "agency" || d.Invoice.AccountName != "=HYPERLINK(\"x\") Agency" || d.Invoice.Store != nil || // noplannames:allow an account's kind, not a plan
		len(d.Lines) != 1 || len(d.Payments) != 0 {
		t.Fatalf("agency's invoice = %+v, %v", d, err)
	}

	for name, err := range map[string]error{
		"no reason": func() error { _, e := f.svc.SettleAccountInvoice(ctx, agencyInvoice, "TRX-9", ""); return e }(),
		"no reference": func() error {
			_, e := f.svc.SettleAccountInvoice(ctx, agencyInvoice, " ", "transfer received")
			return e
		}(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = f.svc.SettleAccountInvoice(ctx, f.ids.New(), "TRX-9", "transfer received"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown invoice: %v", err)
	}
	d, err = f.svc.SettleAccountInvoice(ctx, agencyInvoice, "TRX-9", "transfer received")
	if err != nil || d.Invoice.Status != "paid" || len(d.Payments) != 1 || d.Payments[0].Outcome != "manual" || d.Payments[0].Reference != "TRX-9" ||
		d.Payments[0].AccountName != d.Invoice.AccountName {
		t.Fatalf("settled = %+v, %v", d, err)
	}
	if _, err = f.svc.SettleAccountInvoice(ctx, agencyInvoice, "TRX-10", "again"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("settle twice: %v", err)
	}
	log, err := f.svc.Audit(ctx, app.AuditQuery{Admin: &me.Admin.ID}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(log.Items, func(e app.AuditItem) bool {
		return e.Action == "invoice.settled" && e.TargetID == agencyInvoice.String()
	})
	if i < 0 || log.Items[i].StoreName != "" || !strings.Contains(string(log.Items[i].Metadata), agencyNumber) {
		t.Fatalf("settled entry missing or naming a store: %+v", log.Items)
	}

	pays, err := f.svc.Payments(ctx, billing.PaymentQuery{Outcome: "manual"}, "", billing.MaxListPage)
	if err != nil || !slices.ContainsFunc(pays.Items, func(p billing.ConsolePayment) bool { return p.Invoice == agencyInvoice }) {
		t.Fatalf("payments = %d, %v", len(pays.Items), err)
	}
	if tot, totErr := f.svc.BillingTotals(ctx); totErr != nil || len(tot.Open) == 0 {
		t.Fatalf("totals = %+v, %v", tot, totErr)
	}

	for name, err := range map[string]error{
		"totals":   func() error { _, e := f.svc.BillingTotals(t.Context()); return e }(),
		"invoices": func() error { _, e := f.svc.Invoices(t.Context(), billing.InvoiceQuery{}, "", 10); return e }(),
		"invoice":  func() error { _, e := f.svc.Invoice(t.Context(), storeInvoice); return e }(),
		"payments": func() error { _, e := f.svc.Payments(t.Context(), billing.PaymentQuery{}, "", 10); return e }(),
		"settle":   func() error { _, e := f.svc.SettleAccountInvoice(t.Context(), storeInvoice, "T", "transfer"); return e }(),
		"export":   func() error { _, e := f.svc.ExportInvoices(t.Context(), billing.InvoiceQuery{}); return e }(),
	} {
		if !errs.Is(err, errs.KindUnauthenticated) {
			t.Errorf("not signed in, %s: %v", name, err)
		}
	}
	if _, err = f.cli.Invoices(ctx, billing.InvoiceQuery{}, "", 10); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}

// TestExportInvoices: the CSV holds the list's filtered rows in Cairo
// time with plain decimals, a formula-like name defused, and the export
// is in the admin log with its filters.
func TestExportInvoices(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	_, number := f.seedInvoice(t, nil, "=cmd Agency")

	if _, err := f.svc.ExportInvoices(ctx, billing.InvoiceQuery{Status: "late"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad filter: %v", err)
	}
	ex, err := f.svc.ExportInvoices(ctx, billing.InvoiceQuery{Q: number, Status: "overdue"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if n, writeErr := ex.WriteTo(&buf); writeErr != nil || n != 1 {
		t.Fatalf("wrote %d, %v", n, writeErr)
	}
	raw := buf.String()
	if !strings.HasPrefix(raw, "\ufeff") {
		t.Fatal("no BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	want := []string{number, "", "agency", "'=cmd Agency", "2099-01", "EGP", "100.00", "14.00%", "14.00", "114.00", "overdue", "", ""}
	got := rows[1]
	got[1], got[11] = "", "" // times, checked below
	if !slices.Equal(got, want) {
		t.Fatalf("row = %q; want %q", got, want)
	}
	log, err := f.svc.Audit(ctx, app.AuditQuery{Admin: &me.Admin.ID}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(log.Items, func(e app.AuditItem) bool {
		return e.Action == "billing.invoices_exported" && strings.Contains(string(e.Metadata), `"overdue"`) && strings.Contains(string(e.Metadata), number)
	}) {
		t.Fatal("the export is not in the admin log")
	}
}
