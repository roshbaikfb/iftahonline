package app_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// TestInvoiceJobs runs an annual plan's renewal through the three
// workers, as the worker role does.
func TestInvoiceJobs(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	ctx := b.store(t)
	b.subscribe(ctx, t, "brand", "active", "cycle, period_start, period_end='annual', '2025-11-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	if err := b.svc.AddWorkers(river.NewWorkers()); err != nil {
		t.Fatal(err)
	}
	if app.InvoicesPeriodic() == nil {
		t.Fatal("no periodic job")
	}
	for _, k := range []string{app.InvoiceMonthArgs{}.Kind(), app.InvoiceStoreArgs{}.Kind(), app.InvoiceAccountArgs{}.Kind()} {
		if !strings.HasPrefix(k, "billing.invoice_") {
			t.Errorf("kind %q", k)
		}
	}
	if err := (&app.InvoiceMonthWorker{Svc: b.svc}).Work(t.Context(), &river.Job[app.InvoiceMonthArgs]{}); err != nil {
		t.Fatal(err)
	}
	id, _ := tenant.FromContext(ctx)
	var mine *app.InvoiceStoreArgs
	for _, a := range b.jobs.Args {
		if j, ok := a.(app.InvoiceStoreArgs); ok && j.StoreID == id {
			mine = &j
		}
	}
	if mine == nil {
		t.Fatal("the month's run did not ask for the store's invoice")
	}
	b.jobs.Args = nil
	if err := (&app.InvoiceStoreWorker{Svc: b.svc}).Work(ctx, &river.Job[app.InvoiceStoreArgs]{Args: *mine}); err != nil {
		t.Fatal(err)
	}
	js := b.accountJobs()
	if len(js) != 1 {
		t.Fatalf("%d account jobs", len(js))
	}
	if err := (&app.InvoiceAccountWorker{Svc: b.svc}).Work(t.Context(), &river.Job[app.InvoiceAccountArgs]{Args: js[0]}); err != nil {
		t.Fatal(err)
	}
	got := b.invoices(t, js[0].AccountID)
	if len(got) != 1 || len(got[0].lines) != 1 || got[0].lines[0] != "subscription:1499000" {
		t.Fatalf("annual renewal = %+v", got)
	}
	var desc string
	if err := postgres.InTx(t.Context(), b.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, js[0].AccountID); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT description FROM invoice_lines`).Scan(&desc)
	}); err != nil || !strings.Contains(desc, "براند") || !strings.Contains(desc, "سنوي") {
		t.Fatalf("description %q (%v)", desc, err)
	}
}

// TestInvoicesNeedTheirCollaborators: built without jobs or stores (any
// role but the worker), invoicing refuses rather than half-working.
func TestInvoicesNeedTheirCollaborators(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if err := f.svc.StartMonth(t.Context()); err == nil {
		t.Error("StartMonth without jobs")
	}
	if err := f.svc.InvoiceStore(ctx, october); err == nil {
		t.Error("InvoiceStore without jobs and stores")
	}
	if err := f.svc.InvoiceAccount(t.Context(), f.ids.New(), october); err == nil {
		t.Error("InvoiceAccount without stores")
	}
}
