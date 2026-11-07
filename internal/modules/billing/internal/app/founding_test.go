package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// owners are who owns each store, as identity would say.
type owners struct {
	mu sync.Mutex
	of map[ulid.ID]ulid.ID
}

func (o *owners) set(ctx context.Context, owner ulid.ID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.of[storeOf(ctx)] = owner
}

func (o *owners) read(ctx context.Context, _ pgx.Tx) (ulid.ID, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.of[storeOf(ctx)], nil
}

// TestFoundingMerchants walks the approved terms end to end on real
// billing: the discount on the first period while seats are left, a seat
// once it is paid, one store per owner, the seats running out, the
// discount on each renewal (and the price locked again on an upgrade),
// and the membership lost on leaving or an unpaid renewal. One test: the
// program is the platform's, one for the package's database.
func TestFoundingMerchants(t *testing.T) {
	t.Parallel()
	d, gw := subscribing(t)
	who := &owners{of: map[ulid.ID]ulid.ID{}}
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": subscribeGateway{gw}, "fake": d.gw}, Sealer: plainSealer{}, Holds: d.holds.set,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com",
		Owner: who.read})
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})
	t.Cleanup(func() {
		if offErr := console.SaveFoundingProgram(context.Background(), domain.Program{Seats: 100, DiscountBps: 2000}); offErr != nil {
			t.Error(offErr)
		}
	})

	// choose asks for merchant monthly and returns what the page charges.
	choose := func(ctx context.Context) int64 {
		t.Helper()
		before := len(gw.pages)
		if _, chooseErr := d.svc.Choose(ctx, "merchant", domain.Monthly, app.Payer{}); chooseErr != nil {
			t.Fatal(chooseErr)
		}
		if len(gw.pages) != before+1 {
			t.Fatal("no checkout page")
		}
		return gw.pages[len(gw.pages)-1].AmountMinor
	}
	pay := func(ctx context.Context) ulid.ID {
		t.Helper()
		account := d.accountOf(ctx, t)
		d.own(ctx, account)
		if payErr := d.svc.SettleInvoice(t.Context(), account, d.openInvoice(t, account), "transfer"); payErr != nil {
			t.Fatal(payErr)
		}
		return account
	}
	const full, discounted = 29900 + 4186, 23920 + 3349 // merchant monthly with Egypt's 14% VAT, then less 20%

	// Off by default: the full price.
	a := d.store(t)
	who.set(a, ulid.ID{1})
	if got := choose(a); got != full {
		t.Fatalf("with the program off = %d, want %d", got, full)
	}

	// On, two seats: the first merchant pays 20% less and takes seat 1.
	if err = console.SaveFoundingProgram(t.Context(), domain.Program{Seats: 0, DiscountBps: 2000}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no seats: %v", err)
	}
	if err = console.SaveFoundingProgram(t.Context(), domain.Program{Enabled: true, Seats: 2, DiscountBps: 2000}); err != nil {
		t.Fatal(err)
	}
	if got := choose(a); got != discounted {
		t.Fatalf("seat 1 = %d, want %d", got, discounted)
	}
	accountA := pay(a)
	members, err := console.FoundingMembers(t.Context())
	if err != nil || len(members) != 1 || members[0].StoreID != storeOf(a) || members[0].Seat != 1 || members[0].LockedMinor != 29900 ||
		members[0].PlanCode != "merchant" || !members[0].Active() {
		t.Fatalf("members = %+v, %v", members, err)
	}

	if sum, sumErr := d.svc.Summary(a); sumErr != nil || sum.FoundingSeat != 1 || sum.FoundingBps != 2000 {
		t.Fatalf("A's summary = %+v, %v", sum, sumErr)
	}

	// The same owner's second store: no discount, no seat.
	a2 := d.store(t)
	who.set(a2, ulid.ID{1})
	if got := choose(a2); got != full {
		t.Fatalf("the owner's second store = %d, want %d", got, full)
	}
	pay(a2)

	// Seat 2, then the seats are gone.
	b := d.store(t)
	who.set(b, ulid.ID{2})
	if got := choose(b); got != discounted {
		t.Fatalf("seat 2 = %d", got)
	}
	accountB := pay(b)
	c := d.store(t)
	who.set(c, ulid.ID{3})
	if got := choose(c); got != full {
		t.Fatalf("with the seats gone = %d, want %d", got, full)
	}
	if members, _ = console.FoundingMembers(t.Context()); len(members) != 2 || members[1].StoreID != storeOf(b) || members[1].Seat != 2 {
		t.Fatalf("members = %+v", members)
	}

	// Renewals: the member's discount as a line of its own; an upgrade
	// renews at the new plan's price, locked from then.
	if _, err = d.svc.Choose(a, "pro", domain.Monthly, app.Payer{}); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{a, b} {
		if err = postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
			_, upErr := tx.Exec(ctx, `UPDATE subscriptions SET period_start = '2026-10-01T00:00:00Z', period_end = '2026-11-01T00:00:00Z'`)
			return upErr
		}); err != nil {
			t.Fatal(err)
		}
		if err = d.svc.InvoiceStore(ctx, october); err != nil {
			t.Fatal(err)
		}
	}
	for _, j := range d.accountJobs() {
		if err = d.svc.InvoiceAccount(t.Context(), j.AccountID, j.Period); err != nil {
			t.Fatal(err)
		}
	}
	renewal := func(account ulid.ID) string {
		t.Helper()
		for _, inv := range d.invoices(t, account) {
			if inv.status == "open" {
				return strings.Join(inv.lines, ",")
			}
		}
		return ""
	}
	if got := renewal(accountA); got != "subscription:59900,founding_discount:-11980" {
		t.Fatalf("A's renewal into pro = %q", got)
	}
	if got := renewal(accountB); got != "subscription:29900,founding_discount:-5980" {
		t.Fatalf("B's renewal = %q", got)
	}
	if members, _ = console.FoundingMembers(t.Context()); members[0].PlanCode != "pro" || members[0].LockedMinor != 59900 {
		t.Fatalf("A's lock after the upgrade = %+v", members[0])
	}

	// A pays and leaves for the free plan; B leaves its renewal unpaid
	// past 30 days: both lose the program, and their seats stay taken.
	d.own(a, accountA)
	if err = d.svc.SettleInvoice(t.Context(), accountA, d.openInvoice(t, accountA), "transfer"); err != nil {
		t.Fatal(err)
	}
	if err = postgres.InTenantTx(a, d.pool, func(tx pgx.Tx) error {
		_, upErr := tx.Exec(a, `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'free' AND status = 'active'), cycle = NULL,
			period_start = NULL, period_end = NULL, renewal_invoice_id = NULL, next_plan_id = NULL, next_cycle = NULL`)
		return upErr
	}); err != nil {
		t.Fatal(err)
	}
	*d.clock = runAt.Add(20 * 24 * time.Hour)
	if err = d.svc.FoundingSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if members, _ = console.FoundingMembers(t.Context()); members[0].LostReason != domain.LostCancelled || !members[1].Active() {
		t.Fatalf("after 20 days = %+v", members)
	}
	*d.clock = runAt.Add(40 * 24 * time.Hour)
	if err = (&app.FoundingSweepWorker{Svc: d.svc}).Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if members, _ = console.FoundingMembers(t.Context()); members[1].LostReason != domain.LostUnpaid || members[1].Active() {
		t.Fatalf("after 40 days = %+v", members)
	}
	if sum, sumErr := d.svc.Summary(b); sumErr != nil || sum.FoundingSeat != 0 {
		t.Fatalf("B's summary after leaving = %+v, %v", sum, sumErr)
	}
	if sum, _ := d.svc.Summary(a2); sum.FoundingSeat != 0 {
		t.Fatalf("the owner's second store shows a seat: %+v", sum)
	}
	// Lost seats stay taken: a new merchant pays the full price.
	e := d.store(t)
	who.set(e, ulid.ID{5})
	if got := choose(e); got != full {
		t.Fatalf("after members left = %d, want %d", got, full)
	}
	if p, progErr := console.FoundingProgram(t.Context()); progErr != nil || !p.Enabled || p.Seats != 2 {
		t.Fatalf("program = %+v, %v", p, progErr)
	}
	if seats, seatsErr := console.FoundingSeats(t.Context()); seatsErr != nil || seats.Taken != 2 || seats.Left() != 0 || seats.Open() {
		t.Fatalf("seats = %+v, %v", seats, seatsErr)
	}
	if app.FoundingSweepPeriodic() == nil || (app.FoundingSweepArgs{}).Kind() != "billing.founding_sweep" {
		t.Fatal("the sweep's job")
	}
}

// TestFoundingSweepIsGlobal: the daily sweep's args name their scope, as
// every job's must (jobs.Middleware refuses the insert otherwise, and a
// periodic job refused at start-up held back every other periodic job).
func TestFoundingSweepIsGlobal(t *testing.T) {
	t.Parallel()
	if _, ok := any(app.FoundingSweepArgs{}).(interface{ JobGlobal() }); !ok {
		t.Fatal("FoundingSweepArgs does not embed jobs.GlobalArgs")
	}
}
