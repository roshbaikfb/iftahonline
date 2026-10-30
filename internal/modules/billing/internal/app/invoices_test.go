package app_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	october = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	runAt   = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) // the November run invoices October
)

// billing is a fixture service with invoices' collaborators.
type billing struct {
	fixture
	svc  *app.Service
	jobs *testkit.Jobs
}

func newBilling(t *testing.T) billing {
	t.Helper()
	f := newFixture(t)
	b := billing{fixture: f, jobs: testkit.NewJobs()}
	b.svc = app.NewService(app.Deps{DB: f.pool, Jobs: b.jobs, IDs: f.ids, Now: func() time.Time { return runAt },
		Stores: func(ctx context.Context) (app.StoreInfo, error) {
			id, err := tenant.FromContext(ctx)
			return app.StoreInfo{Name: "متجر " + id.String()[20:], Currency: "EGP"}, err
		}})
	return b
}

// fee adds a delivered order's fee to the store in ctx.
func (b billing) fee(ctx context.Context, t *testing.T, period time.Time, minor int64, status string) {
	t.Helper()
	returned := "NULL"
	if status == "credit_due" {
		returned = "now()"
	}
	if err := postgres.InTenantTx(ctx, b.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sell_fees (store_id, order_id, period, plan_id, base_minor, rate_bps, fee_minor, currency, status, delivered_at, returned_at)
			SELECT app.current_store_id(), $1, $2, id, 1000000, 250, $3, 'EGP', $4, now(), `+returned+` FROM plans WHERE code = 'pays'`,
			b.ids.New(), period, minor, status)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// accountJobs takes the account invoices InvoiceStore asked for.
func (b billing) accountJobs() []app.InvoiceAccountArgs {
	var out []app.InvoiceAccountArgs
	for _, a := range b.jobs.Args {
		if j, ok := a.(app.InvoiceAccountArgs); ok {
			out = append(out, j)
		}
	}
	b.jobs.Args = nil
	return out
}

// invoiceOf runs the store's invoice jobs and returns its account.
func (b billing) invoiceOf(ctx context.Context, t *testing.T) ulid.ID {
	t.Helper()
	if err := b.svc.InvoiceStore(ctx, october); err != nil {
		t.Fatal(err)
	}
	js := b.accountJobs()
	if len(js) != 1 {
		t.Fatalf("%d account jobs", len(js))
	}
	for range 2 { // a retried job writes one invoice
		if err := b.svc.InvoiceAccount(t.Context(), js[0].AccountID, js[0].Period); err != nil {
			t.Fatal(err)
		}
	}
	return js[0].AccountID
}

type invoiceRow struct {
	number               string
	subtotal, tax, total int64
	status, currency     string
	lines                []string // kind:amount
	issued, due          time.Time
}

func (b billing) invoices(t *testing.T, account ulid.ID) []invoiceRow {
	t.Helper()
	var out []invoiceRow
	if err := postgres.InTx(t.Context(), b.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		rows, err := tx.Query(t.Context(), `SELECT i.number, i.subtotal_minor, i.tax_minor, i.total_minor, i.status, i.currency, i.issued_at, i.due_at,
			  (SELECT array_agg(l.kind || ':' || l.amount_minor ORDER BY l.line_no) FROM invoice_lines l WHERE l.invoice_id = i.id)
			FROM invoices i ORDER BY i.number`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r invoiceRow
			if err = rows.Scan(&r.number, &r.subtotal, &r.tax, &r.total, &r.status, &r.currency, &r.issued, &r.due, &r.lines); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func (b billing) feeStatuses(ctx context.Context, t *testing.T) []string {
	t.Helper()
	var out []string
	if err := postgres.InTenantTx(ctx, b.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(array_agg(status || ':' || (invoice_id IS NOT NULL)::text ORDER BY status), '{}') FROM sell_fees`).Scan(&out)
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

var invoiceNumber = regexp.MustCompile(`^INV-2026-\d{6}$`)

// TestInvoiceOfAPayAsYouSellStore: October's fees, a late September fee
// and a credit, with Egypt's VAT; written once, and what it billed is
// marked (ADR-0049).
func TestInvoiceOfAPayAsYouSellStore(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	ctx := b.store(t)
	b.subscribe(ctx, t, "pays", "active", "")
	b.fee(ctx, t, october, 2500, "due")
	b.fee(ctx, t, october, 2500, "due")
	b.fee(ctx, t, october.AddDate(0, -1, 0), 1000, "due") // recorded after September's invoice
	b.fee(ctx, t, october.AddDate(0, 1, 0), 9999, "due")  // November's: the next invoice
	b.fee(ctx, t, october.AddDate(0, -1, 0), 1500, "credit_due")

	account := b.invoiceOf(ctx, t)
	got := b.invoices(t, account)
	if len(got) != 1 {
		t.Fatalf("%d invoices, want 1", len(got))
	}
	inv := got[0]
	if !invoiceNumber.MatchString(inv.number) || inv.subtotal != 4500 || inv.tax != 630 || inv.total != 5130 || inv.status != "open" ||
		inv.currency != "EGP" || !inv.issued.Equal(runAt) || !inv.due.Equal(runAt.Add(72*time.Hour)) {
		t.Fatalf("invoice = %+v", inv)
	}
	if !slices.Equal(inv.lines, []string{"sell_fees:6000", "fee_credit:-1500"}) {
		t.Fatalf("lines = %v", inv.lines)
	}
	if st := b.feeStatuses(ctx, t); !slices.Equal(st, []string{"credited:true", "due:false", "invoiced:true", "invoiced:true", "invoiced:true"}) {
		t.Fatalf("fees = %v", st)
	}
	// The store's account is kept: another month uses it.
	if err := b.svc.InvoiceStore(ctx, october.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if js := b.accountJobs(); len(js) != 1 || js[0].AccountID != account {
		t.Fatalf("the next month's job = %+v", js)
	}
}

// TestInvoiceRenewsAPaidPlanOnce: a monthly plan ending by the month's
// end is billed at its price, once, however many runs.
func TestInvoiceRenewsAPaidPlanOnce(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	ctx := b.store(t)
	b.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := b.invoiceOf(ctx, t)
	got := b.invoices(t, account)
	if len(got) != 1 || !slices.Equal(got[0].lines, []string{"subscription:59900"}) || got[0].total != 59900+8386 {
		t.Fatalf("invoices = %+v", got)
	}
	// Unpaid, the renewal is not billed again in November.
	if err := b.svc.InvoiceStore(ctx, october.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	for _, j := range b.accountJobs() {
		if err := b.svc.InvoiceAccount(t.Context(), j.AccountID, j.Period); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(b.invoices(t, account)); n != 1 {
		t.Fatalf("%d invoices after November, want 1", n)
	}
}

// TestAgencyGetsOneInvoice: an agency's stores are on one invoice, its
// plan billed once, each store's fees on their own line.
func TestAgencyGetsOneInvoice(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	one, two, alone := b.store(t), b.store(t), b.store(t)
	period := "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'"
	for _, ctx := range []context.Context{one, two} {
		b.subscribe(ctx, t, "agency", "active", period)
	}
	b.subscribe(alone, t, "pays", "active", "")
	b.fee(two, t, october, 4000, "due")
	b.fee(alone, t, october, 777, "due")
	agency := b.ids.New()
	if err := postgres.InTx(t.Context(), b.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, agency); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), `INSERT INTO billing_accounts (id, kind, name, currency, country) VALUES ($1, 'agency', 'وكالة', 'EGP', 'EG')`, agency); err != nil {
			return err
		}
		for _, ctx := range []context.Context{one, two} {
			id, _ := tenant.FromContext(ctx)
			if _, err := tx.Exec(t.Context(), `SELECT set_config('app.store_id', $1::uuid::text, true)`, id.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO billing_account_stores (account_id, member_store_id) VALUES ($1, $2)`, agency, id.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(t.Context(), `UPDATE subscriptions SET account_id = $1`, agency); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{one, two} {
		if err := b.svc.InvoiceStore(ctx, october); err != nil {
			t.Fatal(err)
		}
	}
	js := b.accountJobs()
	if len(js) != 2 || js[0] != js[1] || js[0].AccountID != agency {
		t.Fatalf("both stores ask for the agency's invoice: %+v", js)
	}
	if err := b.svc.InvoiceAccount(t.Context(), agency, october); err != nil {
		t.Fatal(err)
	}
	got := b.invoices(t, agency)
	if len(got) != 1 || !slices.Equal(got[0].lines, []string{"subscription:399900", "sell_fees:4000"}) || got[0].subtotal != 403900 {
		t.Fatalf("agency invoice = %+v", got)
	}
	if st := b.feeStatuses(two, t); !slices.Equal(st, []string{"invoiced:true"}) {
		t.Fatalf("agency store's fees = %v", st)
	}
	// Both renewals are billed: neither is billed again.
	for _, ctx := range []context.Context{one, two} {
		var billed bool
		if err := postgres.InTenantTx(ctx, b.pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT renewal_invoice_id IS NOT NULL FROM subscriptions`).Scan(&billed)
		}); err != nil || !billed {
			t.Fatalf("renewal billed = %v (%v)", billed, err)
		}
	}
	// The store outside the agency is not on its invoice.
	if st := b.feeStatuses(alone, t); !slices.Equal(st, []string{"due:false"}) {
		t.Fatalf("the other store's fee = %v", st)
	}
}

// TestNothingToBill: no charges, or credits alone, write no invoice; the
// credits wait.
func TestNothingToBill(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	empty, credits, unplanned := b.store(t), b.store(t), b.store(t)
	b.subscribe(empty, t, "pays", "active", "")
	b.subscribe(credits, t, "pays", "active", "")
	b.fee(credits, t, october, 500, "credit_due")
	for _, ctx := range []context.Context{empty, credits} {
		if got := b.invoices(t, b.invoiceOf(ctx, t)); len(got) != 0 {
			t.Fatalf("invoices = %+v", got)
		}
	}
	if st := b.feeStatuses(credits, t); !slices.Equal(st, []string{"credit_due:false"}) {
		t.Fatalf("credits = %v", st)
	}
	// A store without a subscription has no account and nothing to bill.
	if err := b.svc.InvoiceStore(unplanned, october); err != nil || len(b.accountJobs()) != 0 {
		t.Fatalf("no subscription: %v", err)
	}
}

// TestStartMonth asks for the previous month's invoice of the stores
// with something to bill, and only those.
func TestStartMonth(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	fees, renews, quiet := b.store(t), b.store(t), b.store(t)
	b.subscribe(fees, t, "pays", "active", "")
	b.fee(fees, t, october, 100, "due")
	b.subscribe(renews, t, "brand", "active", "cycle, period_start, period_end='annual', '2025-11-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	b.subscribe(quiet, t, "merchant", "active", "cycle, period_start, period_end='monthly', '2026-10-15T00:00:00Z', '2026-12-01T00:00:00Z'")
	if err := b.svc.StartMonth(t.Context()); err != nil {
		t.Fatal(err)
	}
	asked := map[tenant.StoreID]bool{}
	for _, a := range b.jobs.Args {
		if j, ok := a.(app.InvoiceStoreArgs); ok && j.Period.Equal(october) {
			asked[j.StoreID] = true
		}
	}
	for ctx, want := range map[context.Context]bool{fees: true, renews: true, quiet: false} {
		id, _ := tenant.FromContext(ctx)
		if asked[id] != want {
			t.Errorf("store %s asked = %v, want %v", id, asked[id], want)
		}
	}
}

// TestAccountWall: an account's invoices are behind its wall: another
// account and no account see none, and merchant code cannot delete them.
func TestAccountWall(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	ctx := b.store(t)
	b.subscribe(ctx, t, "pays", "active", "")
	b.fee(ctx, t, october, 2500, "due")
	account := b.invoiceOf(ctx, t)
	if len(b.invoices(t, account)) != 1 {
		t.Fatal("no invoice")
	}
	if got := b.invoices(t, b.ids.New()); len(got) != 0 {
		t.Fatalf("another account sees %d invoices", len(got))
	}
	for _, q := range []string{`SELECT count(*) FROM invoices`, `SELECT count(*) FROM invoice_lines`, `SELECT count(*) FROM billing_accounts`} {
		var n int
		if err := b.pool.QueryRow(t.Context(), q).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s without an account = %d (%v)", q, n, err)
		}
	}
	err := postgres.InTx(t.Context(), b.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `DELETE FROM invoices`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("delete an invoice: %v", err)
	}
}

// TestAccountTablesUseTheWall: every account-owned table forces RLS
// with the account_isolation policy, for reads and writes.
func TestAccountTablesUseTheWall(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rows, err := f.pool.Query(t.Context(), `
		SELECT c.relname, c.relrowsecurity AND c.relforcerowsecurity,
		       coalesce(pg_get_expr(p.polqual, p.polrelid), ''), coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '')
		FROM pg_class c LEFT JOIN pg_policy p ON p.polrelid = c.oid AND p.polname = 'account_isolation'
		WHERE c.relname IN ('billing_accounts', 'billing_account_stores', 'invoices', 'invoice_lines') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name, using, check string
		var forced bool
		if err = rows.Scan(&name, &forced, &using, &check); err != nil {
			t.Fatal(err)
		}
		n++
		if !forced || !strings.Contains(using, "app.current_account_id()") || using != check {
			t.Errorf("%s: forced %v, USING %q, WITH CHECK %q", name, forced, using, check)
		}
	}
	if n != 4 {
		t.Fatalf("%d account tables", n)
	}
}
