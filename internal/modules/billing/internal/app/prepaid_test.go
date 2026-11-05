package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// prepaid is the dunning fixture with an order book, for a store paying
// its fees from a prepaid balance.
type prepaid struct {
	*dunning
	book *orderBook
}

func newPrepaid(t *testing.T) *prepaid {
	t.Helper()
	d := newDunning(t)
	p := &prepaid{dunning: d, book: &orderBook{orders: map[ulid.ID]app.Order{}}}
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Orders: p.book.get,
		Stores: func(ctx context.Context) (app.StoreInfo, error) {
			id, err := tenant.FromContext(ctx)
			return app.StoreInfo{Name: "متجر " + id.String()[20:], Currency: "EGP"}, err
		},
		Gateways: map[string]app.Gateway{"fake": d.gw}, Sealer: plainSealer{}, Holds: d.holds.set,
		Notifier: func(context.Context, app.Notice) error { return nil }})
	return p
}

// choose makes the store-in-ctx's account pay from its balance (the
// merchant's choice is F5's).
func (p *prepaid) choose(ctx context.Context, t *testing.T, account ulid.ID) {
	t.Helper()
	if err := postgres.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE billing_accounts SET collection = 'balance'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// move sets an order's status and runs billing's handler, twice: an
// event can arrive more than once.
func (p *prepaid) move(ctx context.Context, t *testing.T, id ulid.ID, st app.OrderStatus, base int64, at time.Time) {
	t.Helper()
	p.book.set(id, app.Order{Status: st, Base: egp(t, base), StatusAt: at})
	for range 2 {
		if err := p.svc.OrderMoved(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}

func (p *prepaid) notices() []domain.NoticeKind {
	var out []domain.NoticeKind
	for _, a := range p.jobs.Args {
		if n, ok := a.(app.NotifyArgs); ok {
			out = append(out, n.Notice.Kind)
		}
	}
	p.jobs.Args = nil
	return out
}

func (p *prepaid) rate(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	var bps int64
	if err := postgres.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT f.rate_bps FROM plan_fees f JOIN plans p ON p.id = f.plan_id
			WHERE p.kind = 'pays' AND p.status = 'active' AND f.currency = 'EGP'`).Scan(&bps)
	}); err != nil {
		t.Fatal(err)
	}
	return bps
}

func sameKinds(got []domain.NoticeKind, want ...domain.NoticeKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPrepaidBalance is the approved flow (ADR-0084): each delivered
// order's fee is taken from the balance once, the balance falling under
// half the minimum is told, running out puts the store past due, a
// return puts the fee back at once and brings the store back, and the
// month's invoice is paid from the balance, which gives only its tax.
func TestPrepaidBalance(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	p.own(ctx, ulid.ID{})
	admin := p.ids.New()
	credit, _ := domain.ParseCredit(30000, "transfer", "CIB 1")
	if _, err := p.svc.CreditBalance(ctx, credit, admin); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)
	p.choose(ctx, t, account)
	p.jobs.Args = nil

	// An order of 200 fees to pays: 2/3 of 300 at any rate (the base is
	// set from the plan's rate).
	rate := p.rate(ctx, t)
	base := 20000 * 10000 / rate
	delivered := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	a, b := p.ids.New(), p.ids.New()

	// Order A: 300 - 200 = 100, under half the minimum (150): told.
	p.move(ctx, t, a, app.OrderDelivered, base, delivered)
	bal, err := p.svc.StoreBalance(ctx)
	if err != nil || bal.Minor != 10000 || bal.Level != domain.BalanceLow || len(bal.Entries) != 2 || bal.Entries[0].Kind != domain.EntryFee ||
		bal.Entries[0].Order != a {
		t.Fatalf("after A = %+v, %v", bal, err)
	}
	if n := p.notices(); !sameKinds(n, domain.NoticeBalanceLow) {
		t.Fatalf("after A notices = %v", n)
	}
	if fees := p.fees(ctx, t); fees[a].fee != 20000 {
		t.Fatalf("fee A = %+v", fees[a])
	}

	// Order B: 100 - 200 = -100: past due for 7 days, told.
	p.move(ctx, t, b, app.OrderDelivered, base, delivered.Add(time.Hour))
	if bal, _ = p.svc.StoreBalance(ctx); bal.Minor != -10000 || bal.Level != domain.BalanceDebt {
		t.Fatalf("after B = %+v", bal)
	}
	st, grace, _, _ := p.state(ctx, t)
	if st != "past_due" || grace == nil || !grace.Equal(runAt.Add(7*24*time.Hour)) {
		t.Fatalf("after B: %s until %v", st, grace)
	}
	if n := p.notices(); !sameKinds(n, domain.NoticeBalanceEmpty) {
		t.Fatalf("after B notices = %v", n)
	}

	// A comes back before the invoice: its 200 back, once; the store too.
	p.move(ctx, t, a, app.OrderReturned, base, delivered.Add(2*time.Hour))
	if bal, _ = p.svc.StoreBalance(ctx); bal.Minor != 10000 || bal.Entries[0].Kind != domain.EntryFeeRefund {
		t.Fatalf("after A's return = %+v", bal)
	}
	if st, _, _, _ = p.state(ctx, t); st != "active" || p.holds.of(ctx) {
		t.Fatalf("after A's return: %s", st)
	}
	if n := p.notices(); !sameKinds(n, domain.NoticeBalanceRestored) {
		t.Fatalf("after A's return notices = %v", n)
	}

	// October's invoice: B's 200 and its tax (14%: 28). Paid from the
	// balance, which gives the 28 alone; nothing charged on a card.
	if err = p.svc.InvoiceAccount(t.Context(), account, october); err != nil {
		t.Fatal(err)
	}
	if err = p.svc.InvoiceAccount(t.Context(), account, october); err != nil { // a retried job: one invoice
		t.Fatal(err)
	}
	invs := p.invoices(t, account)
	if len(invs) != 1 || invs[0].subtotal != 20000 || invs[0].tax != 2800 || invs[0].status != "paid" {
		t.Fatalf("invoices = %+v", invs)
	}
	if bal, _ = p.svc.StoreBalance(ctx); bal.Minor != 10000-2800 || bal.Entries[0].Kind != domain.EntryInvoice || bal.Entries[0].AmountMinor != -2800 ||
		bal.Entries[0].Reference != invs[0].number {
		t.Fatalf("after the invoice = %+v", bal)
	}
	if n := p.notices(); !sameKinds(n, domain.NoticePaid) {
		t.Fatalf("invoice notices = %v", n)
	}
	before := len(p.gw.charges)
	if p.tick(t, runAt.Add(time.Hour)); len(p.gw.charges) != before {
		t.Fatal("a paid-from-balance invoice was charged")
	}

	// B comes back after the invoice: its 200 back at once; the next
	// invoice credits it, and the balance is not given it twice.
	p.move(ctx, t, b, app.OrderReturned, base, runAt.Add(2*time.Hour))
	if bal, _ = p.svc.StoreBalance(ctx); bal.Minor != 7200+20000 {
		t.Fatalf("after B's return = %d", bal.Minor)
	}
	// The balance always equals what was paid in less what the invoices took.
	var entries int64
	for _, e := range bal.Entries {
		entries += e.AmountMinor
	}
	if entries != bal.Minor {
		t.Fatalf("entries sum to %d, balance %d", entries, bal.Minor)
	}
}

// TestPrepaidOnlyForBalanceAccounts: an account that has not chosen the
// balance pays its fees on the monthly invoice, as before.
func TestPrepaidOnlyForBalanceAccounts(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	credit, _ := domain.ParseCredit(30000, "transfer", "CIB 2")
	if _, err := p.svc.CreditBalance(ctx, credit, p.ids.New()); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)
	o := p.ids.New()
	p.move(ctx, t, o, app.OrderDelivered, 100000, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if bal, err := p.svc.StoreBalance(ctx); err != nil || bal.Minor != 30000 || len(bal.Entries) != 1 {
		t.Fatalf("balance = %+v, %v", bal, err)
	}
	if err := p.svc.InvoiceAccount(t.Context(), account, october); err != nil {
		t.Fatal(err)
	}
	if invs := p.invoices(t, account); len(invs) != 1 || invs[0].status != "open" {
		t.Fatalf("invoices = %+v", invs)
	}
}

// invoiceAccount is the store-in-ctx's account (CreditBalance opened it).
func (p *prepaid) invoiceAccount(ctx context.Context, t *testing.T) ulid.ID {
	t.Helper()
	var id ulid.ID
	if err := postgres.InTenantTx(ctx, p.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT account_id FROM subscriptions`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	p.mine[id] = true
	return id
}
