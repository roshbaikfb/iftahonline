package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeGateway approves every card token but those starting "decline".
type fakeGateway struct {
	mu      sync.Mutex
	charges []app.Charge
	broken  bool
}

func (g *fakeGateway) Charge(_ context.Context, c app.Charge) (app.ChargeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.broken {
		return app.ChargeResult{}, errors.New("gateway unreachable")
	}
	g.charges = append(g.charges, c)
	if strings.HasPrefix(c.Token, "decline") {
		return app.ChargeResult{Reason: "insufficient funds"}, nil
	}
	return app.ChargeResult{Approved: true, Reference: "txn-" + c.Reference}, nil
}

// plainSealer seals by prefixing the AAD, so a token opens only for its
// account.
type plainSealer struct{}

func (plainSealer) Seal(_ context.Context, p, aad []byte) ([]byte, error) {
	return append(append(append([]byte{}, aad...), '|'), p...), nil
}

func (plainSealer) Open(_ context.Context, b, aad []byte) ([]byte, error) {
	want := append(append([]byte{}, aad...), '|')
	if !strings.HasPrefix(string(b), string(want)) {
		return nil, errors.New("wrong aad")
	}
	return b[len(want):], nil
}

// holds records each store's billing hold.
type holds struct {
	mu sync.Mutex
	on map[tenant.StoreID]bool
}

func (h *holds) set(ctx context.Context, tx pgx.Tx, on bool) error {
	if tx == nil {
		return errors.New("a hold outside a transaction")
	}
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.on[id] = on
	return nil
}

func (h *holds) of(ctx context.Context) bool {
	id, _ := tenant.FromContext(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.on[id]
}

// dunning is a billing fixture with a clock, a gateway, holds and the
// jobs it runs by hand.
type dunning struct {
	billing
	// mine are the test's accounts and stores: the database is shared
	// by parallel tests, whose due work the hourly run also lists.
	mine   map[ulid.ID]bool
	clock  *time.Time
	gw     *fakeGateway
	holds  *holds
	events *testkit.Events
}

func newDunning(t *testing.T) *dunning {
	t.Helper()
	b := newBilling(t)
	at := runAt
	d := &dunning{billing: b, mine: map[ulid.ID]bool{}, clock: &at, gw: &fakeGateway{}, holds: &holds{on: map[tenant.StoreID]bool{}}, events: testkit.NewEvents()}
	d.svc = app.NewService(app.Deps{DB: b.pool, Jobs: b.jobs, IDs: b.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(ctx context.Context) (app.StoreInfo, error) {
			id, err := tenant.FromContext(ctx)
			return app.StoreInfo{Name: "متجر " + id.String()[20:], Currency: "EGP"}, err
		},
		Gateways: map[string]app.Gateway{"fake": d.gw}, Sealer: plainSealer{}, Holds: d.holds.set,
		Notifier: func(context.Context, app.Notice) error { return nil }})
	return d
}

// run runs the jobs enqueued so far, as the worker would, until none is
// left; it returns the notices sent.
func (d *dunning) run(t *testing.T) []domain.NoticeKind {
	t.Helper()
	var notices []domain.NoticeKind
	for len(d.jobs.Args) > 0 {
		pending := d.jobs.Args
		d.jobs.Args = nil
		for _, a := range pending {
			var err error
			switch j := a.(type) {
			case app.ChargeInvoiceArgs:
				if !d.mine[j.AccountID] {
					continue
				}
				err = (&app.ChargeInvoiceWorker{Svc: d.svc}).Work(t.Context(), &river.Job[app.ChargeInvoiceArgs]{Args: j})
			case app.SuspendArgs:
				if !d.mine[j.StoreID.ID] {
					continue
				}
				err = (&app.SuspendWorker{Svc: d.svc}).Work(tenant.WithStore(t.Context(), j.StoreID), &river.Job[app.SuspendArgs]{Args: j})
			case app.CancelArgs:
				if !d.mine[j.StoreID.ID] {
					continue
				}
				err = (&app.CancelWorker{Svc: d.svc}).Work(tenant.WithStore(t.Context(), j.StoreID), &river.Job[app.CancelArgs]{Args: j})
			case app.NotifyArgs:
				err = (&app.NotifyWorker{Svc: d.svc}).Work(tenant.WithStore(t.Context(), j.StoreID), &river.Job[app.NotifyArgs]{Args: j})
				notices = append(notices, j.Notice.Kind)
			case app.InvoiceAccountArgs:
				err = d.svc.InvoiceAccount(t.Context(), j.AccountID, j.Period)
			}
			if err != nil {
				t.Fatalf("%s: %v", a.Kind(), err)
			}
		}
	}
	return notices
}

// own marks the store in ctx, and its account once it has one, as the
// test's.
func (d *dunning) own(ctx context.Context, account ulid.ID) {
	id, _ := tenant.FromContext(ctx)
	d.mine[id.ID] = true
	if !account.IsZero() {
		d.mine[account] = true
	}
}

// tick moves the clock to at and runs the hourly dunning and its jobs.
func (d *dunning) tick(t *testing.T, at time.Time) []domain.NoticeKind {
	t.Helper()
	*d.clock = at
	if err := (&app.DunningWorker{Svc: d.svc}).Work(t.Context(), &river.Job[app.DunningArgs]{}); err != nil {
		t.Fatal(err)
	}
	return d.run(t)
}

func (d *dunning) state(ctx context.Context, t *testing.T) (status string, grace, periodEnd *time.Time, renewalBilled bool) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status, grace_until, period_end, renewal_invoice_id IS NOT NULL FROM subscriptions`).
			Scan(&status, &grace, &periodEnd, &renewalBilled)
	}); err != nil {
		t.Fatal(err)
	}
	return status, grace, periodEnd, renewalBilled
}

func (d *dunning) invoice(t *testing.T, account ulid.ID) (status string, attempts int, next *time.Time) {
	t.Helper()
	if err := postgres.InTx(t.Context(), d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT status, attempts, next_attempt_at FROM invoices`).Scan(&status, &attempts, &next)
	}); err != nil {
		t.Fatal(err)
	}
	return status, attempts, next
}

func (d *dunning) adFree(ctx context.Context, t *testing.T) bool {
	t.Helper()
	g, err := d.svc.Grants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return g["ad_free"].Value == 1
}

// TestDunningScenario is P7's acceptance scenario: a renewal's charge
// fails, is retried on days 1, 3 and 5, the 7-day grace runs out, the
// store is suspended (held, the free plan's grants), and paying restores
// it with its next period (ADR-0051).
func TestDunningScenario(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if err := d.svc.SavePaymentMethod(t.Context(), account, "fake", "decline-card", "Visa •••• 2346"); err != nil {
		t.Fatal(err)
	}

	// Day 0: the first charge fails. Past due, still on pro.
	day0 := runAt.Add(time.Hour)
	if n := d.tick(t, day0); len(n) != 1 || n[0] != domain.NoticePaymentFailed {
		t.Fatalf("day 0 notices = %v", n)
	}
	st, grace, _, _ := d.state(ctx, t)
	if st != "past_due" || grace == nil || !grace.Equal(day0.Add(7*24*time.Hour)) || !d.adFree(ctx, t) || d.holds.of(ctx) {
		t.Fatalf("day 0: %s, grace %v", st, grace)
	}
	if s, n, next := d.invoice(t, account); s != "open" || n != 1 || next == nil || !next.Equal(day0.Add(24*time.Hour)) {
		t.Fatalf("day 0 invoice: %s %d %v", s, n, next)
	}

	// Days 1, 3 and 5: the retries fail too; nothing happens in between.
	for i, offset := range []time.Duration{24, 72, 120} {
		at := day0.Add(offset * time.Hour)
		if n := d.tick(t, at.Add(-time.Minute)); len(n) != 0 {
			t.Fatalf("before retry %d: %v", i+1, n)
		}
		if n := d.tick(t, at); len(n) != 1 || n[0] != domain.NoticePaymentFailed {
			t.Fatalf("retry %d notices = %v", i+1, n)
		}
	}
	if s, n, next := d.invoice(t, account); s != "open" || n != 4 || next != nil {
		t.Fatalf("after the retries: %s %d attempts, next %v", s, n, next)
	}
	if len(d.gw.charges) != 4 || d.gw.charges[0].AmountMinor != 59900+8386 || !strings.HasPrefix(d.gw.charges[0].Reference, "INV-") {
		t.Fatalf("charges = %+v", d.gw.charges)
	}
	if st, _, _, _ = d.state(ctx, t); st != "past_due" {
		t.Fatalf("day 5: %s", st)
	}

	// Day 7: the grace runs out. Suspended, held, on the free plan's grants.
	if n := d.tick(t, day0.Add(7*24*time.Hour)); len(n) != 1 || n[0] != domain.NoticeSuspended {
		t.Fatalf("day 7 notices = %v", n)
	}
	if st, _, _, _ = d.state(ctx, t); st != "suspended" || !d.holds.of(ctx) || d.adFree(ctx, t) {
		t.Fatalf("day 7: %s, held %v", st, d.holds.of(ctx))
	}

	// Day 10: the merchant changes the card and pays. Restored, with the
	// period the invoice renewed.
	*d.clock = day0.Add(10 * 24 * time.Hour)
	if err := d.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Mastercard •••• 0008"); err != nil {
		t.Fatal(err)
	}
	if err := d.svc.ChargeInvoice(t.Context(), account, d.invoiceID(t, account)); err != nil {
		t.Fatal(err)
	}
	if n := d.run(t); len(n) != 1 || n[0] != domain.NoticePaid {
		t.Fatalf("payment notices = %v", n)
	}
	st, grace, end, billed := d.state(ctx, t)
	if st != "active" || grace != nil || end == nil || !end.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) || billed {
		t.Fatalf("restored: %s, grace %v, period end %v, renewal billed %v", st, grace, end, billed)
	}
	if s, _, _ := d.invoice(t, account); s != "paid" || d.holds.of(ctx) || !d.adFree(ctx, t) {
		t.Fatalf("after payment: invoice %s, held %v", s, d.holds.of(ctx))
	}
	// Every step was a SubscriptionChanged: past due, suspended, active.
	if n := len(d.events.All()); n != 3 {
		t.Fatalf("%d subscription events, want 3", n)
	}
	// Paid: later runs charge nothing.
	before := len(d.gw.charges)
	if d.tick(t, day0.Add(30*24*time.Hour)); len(d.gw.charges) != before {
		t.Fatal("a paid invoice was charged again")
	}
}

func (d *dunning) invoiceID(t *testing.T, account ulid.ID) ulid.ID {
	t.Helper()
	var id ulid.ID
	if err := postgres.InTx(t.Context(), d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT id FROM invoices`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPaidOnFirstTry: an approved charge pays the invoice and renews the
// period, with one notice.
func TestPaidOnFirstTry(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if err := d.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Visa •••• 4242"); err != nil {
		t.Fatal(err)
	}
	if n := d.tick(t, runAt.Add(time.Hour)); len(n) != 1 || n[0] != domain.NoticePaid {
		t.Fatalf("notices = %v", n)
	}
	if st, _, end, _ := d.state(ctx, t); st != "active" || !end.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("after payment: %s until %v", st, end)
	}
}

// TestNoCardOnFile: without a card the attempt fails like a decline (the
// merchant is told to pay), and a gateway that cannot be reached records
// nothing: the job is retried.
func TestNoCardOnFile(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pays", "active", "")
	d.fee(ctx, t, october, 2500, "due")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if n := d.tick(t, runAt.Add(time.Hour)); len(n) != 1 || n[0] != domain.NoticePaymentFailed {
		t.Fatalf("notices = %v", n)
	}
	if st, _, _, _ := d.state(ctx, t); st != "past_due" {
		t.Fatalf("pay as you sell unpaid: %s", st)
	}
	if err := d.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Visa •••• 4242"); err != nil {
		t.Fatal(err)
	}
	d.gw.broken = true
	if err := d.svc.ChargeInvoice(t.Context(), account, d.invoiceID(t, account)); err == nil {
		t.Fatal("an unreachable gateway: want an error (the job retries)")
	}
	if _, n, _ := d.invoice(t, account); n != 1 {
		t.Fatalf("%d attempts, want 1: an unknown outcome is not a failure", n)
	}
	// Paid by transfer, confirmed by the platform.
	if err := d.svc.SettleInvoice(t.Context(), account, d.invoiceID(t, account), "bank-transfer-123"); err != nil {
		t.Fatal(err)
	}
	if n := d.run(t); len(n) != 1 || n[0] != domain.NoticePaid {
		t.Fatalf("settle notices = %v", n)
	}
	if st, _, _, _ := d.state(ctx, t); st != "active" {
		t.Fatalf("after the transfer: %s", st)
	}
}

// TestCancelAfterRetention: a store suspended for 90 days is cancelled
// (its data may be deleted), and stays held.
func TestCancelAfterRetention(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	ctx := d.store(t)
	suspendedAt := runAt.Add(-91 * 24 * time.Hour).Format(time.RFC3339)
	d.subscribe(ctx, t, "merchant", "suspended", "suspended_at, cycle='"+suspendedAt+"', 'monthly'")
	d.own(ctx, ulid.ID{})
	if n := d.tick(t, runAt); len(n) != 0 {
		t.Fatalf("notices = %v", n)
	}
	if st, _, _, _ := d.state(ctx, t); st != "cancelled" {
		t.Fatalf("after the retention: %s", st)
	}
}

func TestDunningNeedsItsCollaborators(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.svc.Dunning(t.Context()); err == nil {
		t.Error("Dunning without jobs")
	}
	if err := f.svc.Notify(t.Context(), app.Notice{}); err == nil {
		t.Error("Notify without a notifier")
	}
	if err := f.svc.SavePaymentMethod(t.Context(), f.ids.New(), "fake", "t", "l"); err == nil {
		t.Error("SavePaymentMethod without a sealer")
	}
	for _, k := range []string{app.DunningArgs{}.Kind(), app.ChargeInvoiceArgs{}.Kind(), app.SuspendArgs{}.Kind(), app.CancelArgs{}.Kind(), app.NotifyArgs{}.Kind()} {
		if !strings.HasPrefix(k, "billing.") {
			t.Errorf("kind %q", k)
		}
	}
	if app.DunningPeriodic() == nil {
		t.Error("no periodic job")
	}
}
