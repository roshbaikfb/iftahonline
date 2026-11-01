package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/risktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type fixture struct {
	svc    *app.Service
	pool   *pgxpool.Pool
	ids    *ulid.Generator
	orders *risktest.Orders
	jobs   *testkit.Jobs
	plan   *plan
	now    time.Time
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return newFixtureOn(t, pool)
}

func newFixtureOn(t testing.TB, db *pgxpool.Pool) *fixture {
	t.Helper()
	keyer, err := domain.NewKeyer([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	ids, now := ulid.NewGenerator(), time.Now().UTC().Truncate(time.Second)
	f := &fixture{pool: db, ids: ids, orders: risktest.NewOrders(ids, now), jobs: testkit.NewJobs(), plan: &plan{}, now: now}
	f.plan.rules.Store(true)
	f.svc = app.NewService(app.Deps{DB: db, Orders: f.orders, Keyer: keyer, Jobs: f.jobs, Plan: f.plan, Now: func() time.Time { return f.now }})
	return f
}

func (f *fixture) store(t testing.TB) context.Context {
	t.Helper()
	ctx, _ := risktest.Store(t, f.pool, f.ids)
	return ctx
}

func (f *fixture) uniquePhone(t testing.TB) phone.E164 { t.Helper(); return risktest.Phone(t, f.ids) }

func (f *fixture) order(ctx context.Context, p phone.E164, daysAgo int, st orders.Status, reason orders.OutcomeReason) orders.Order {
	return f.orders.Order(ctx, p, daysAgo, st, reason)
}

func (f *fixture) learn(ctx context.Context, t testing.TB, o orders.Order) {
	t.Helper()
	if err := f.svc.Learn(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
}

// TestLearnAndBands follows one customer through three stores: two
// refusals elsewhere make them high in the third, whose own history
// shows only its own orders.
func TestLearnAndBands(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b, c := f.store(t), f.store(t), f.store(t)
	p := f.uniquePhone(t)

	// A new customer.
	oc := f.order(c, p, 0, orders.StatusPending, orders.ReasonNone)
	f.learn(c, t, oc)
	r, err := f.svc.ForOrder(c, oc.ID)
	if err != nil || r.Band != domain.BandNew || r.History.Orders != 1 || !r.History.LastAt.IsZero() {
		t.Fatalf("new customer: %+v, %v", r, err)
	}

	// Refused in A, returned in B.
	oa := f.order(a, p, 5, orders.StatusShipped, orders.ReasonNone)
	f.learn(a, t, oa)
	oa.Status, oa.OutcomeReason = orders.StatusReturned, orders.ReasonRefused
	f.orders.Put(a, oa)
	f.learn(a, t, oa)
	f.learn(a, t, oa) // a redelivered event
	ob := f.order(b, p, 20, orders.StatusReturned, orders.ReasonNone)
	f.learn(b, t, ob)

	bands, err := f.svc.Bands(c, []ulid.ID{oc.ID, oa.ID, ob.ID})
	if err != nil || len(bands) != 1 || bands[oc.ID] != domain.BandHigh {
		t.Fatalf("store C's bands = %v, %v (only its own order, high)", bands, err)
	}
	r, err = f.svc.ForOrder(c, oc.ID)
	if err != nil || r.Band != domain.BandHigh || r.History.Orders != 1 || r.History.Outcomes[domain.Refused] != 0 {
		t.Fatalf("store C sees %+v, %v", r, err)
	}
	// A sees its own refusal in its history.
	r, err = f.svc.ForOrder(a, oa.ID)
	if err != nil || r.History.Orders != 1 || r.History.Outcomes[domain.Refused] != 1 || r.History.LastAt.IsZero() {
		t.Fatalf("store A sees %+v, %v", r, err)
	}
	// Another store's order is not found, whoever asks.
	if _, err = f.svc.ForOrder(c, oa.ID); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("store C reading A's order: %v", err)
	}
	if err = f.svc.Learn(b, oa.ID); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("store B learning A's order: %v", err)
	}

	// Each store's past was queued once.
	if n := countOf(f.jobs.Kinds(), "risk.backfill"); n != 3 {
		t.Fatalf("backfills queued: %d, want 3", n)
	}
}

func countOf(kinds []string, kind string) int {
	n := 0
	for _, k := range kinds {
		if k == kind {
			n++
		}
	}
	return n
}

// TestForOrderReadsLateOrders (P8 review M6): an order whose event has
// not arrived yet is scored by its phone from what is known, and asking
// writes nothing; its event then teaches it.
func TestForOrderReadsLateOrders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.store(t)
	p := f.uniquePhone(t)
	before := f.order(a, p, 1, orders.StatusDelivered, orders.ReasonNone)
	if err := f.svc.Learn(a, before.ID); err != nil {
		t.Fatal(err)
	}
	o := f.order(a, p, 2, orders.StatusDelivered, orders.ReasonNone)
	r, err := f.svc.ForOrder(a, o.ID)
	if err != nil || r.Band != domain.BandLow || r.History.Orders != 1 || r.History.Outcomes[domain.Delivered] != 1 {
		t.Fatalf("scored before it is learnt: %+v, %v", r, err)
	}
	if bands, bErr := f.svc.Bands(a, []ulid.ID{o.ID}); bErr != nil || len(bands) != 0 {
		t.Fatalf("asking learnt it: %v, %v", bands, bErr)
	}
	if err = f.svc.Learn(a, o.ID); err != nil {
		t.Fatal(err)
	}
	if r, err = f.svc.ForOrder(a, o.ID); err != nil || r.History.Orders != 2 || r.History.Outcomes[domain.Delivered] != 2 {
		t.Fatalf("after its event: %+v, %v", r, err)
	}
	if _, err = f.svc.ForOrder(a, f.ids.New()); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("unknown order: %v", err)
	}
}

func TestBandsValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.store(t)
	for _, n := range []int{0, app.MaxBands + 1} {
		ids := make([]ulid.ID, n)
		for i := range ids {
			ids[i] = f.ids.New()
		}
		if _, err := f.svc.Bands(a, ids); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("%d orders: %v", n, err)
		}
	}
	if _, err := f.svc.Bands(t.Context(), []ulid.ID{f.ids.New()}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("without a tenant: %v", err)
	}
}

// TestBandsQueries: a page of orders costs the same queries whatever its
// size (no N+1).
func TestBandsQueries(t *testing.T) {
	t.Parallel()
	pool, qc := testkit.CountingPool(t, infra.AppURL)
	f := newFixtureOn(t, pool)
	a := f.store(t)
	count := func(n int) int64 {
		t.Helper()
		ids := make([]ulid.ID, n)
		for i := range ids {
			o := f.order(a, f.uniquePhone(t), i, orders.StatusDelivered, orders.ReasonNone)
			f.learn(a, t, o)
			ids[i] = o.ID
		}
		qc.Reset()
		bands, err := f.svc.Bands(a, ids)
		if err != nil || len(bands) != n {
			t.Fatalf("%d bands: %v, %v", len(bands), bands, err)
		}
		return qc.Count()
	}
	if one, many := count(1), count(app.MaxBands); one != many {
		t.Fatalf("queries for 1 order: %d, for %d: %d", one, app.MaxBands, many)
	}
}

// TestBackfill learns a store's whole past, page after page, once.
func TestBackfill(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.store(t), f.store(t)
	p := f.uniquePhone(t)
	var last orders.Order
	for i := range 230 {
		st := orders.StatusDelivered
		if i%10 == 0 {
			st = orders.StatusReturned
		}
		last = f.order(a, p, 300-i, st, orders.ReasonNone)
	}
	f.order(b, p, 1, orders.StatusPending, orders.ReasonNone)
	n, err := f.svc.Backfill(a)
	if err != nil || n != 230 {
		t.Fatalf("backfilled %d, %v", n, err)
	}
	if n, err = f.svc.Backfill(a); err != nil || n != 230 {
		t.Fatalf("again: %d, %v", n, err)
	}
	r, err := f.svc.ForOrder(a, last.ID)
	if err != nil || r.History.Orders != 230 || r.History.Outcomes[domain.Returned] != 23 || r.History.Outcomes[domain.Delivered] != 207 {
		t.Fatalf("history after the backfill: %+v, %v", r, err)
	}
	if _, err = f.svc.Backfill(t.Context()); err == nil {
		t.Fatal("a backfill needs a tenant")
	}
}

// TestPurge drops outcomes past the horizon and keeps the rest.
func TestPurge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.store(t)
	p := f.uniquePhone(t)
	old := f.order(a, p, 800, orders.StatusReturned, orders.ReasonNone)
	recent := f.order(a, p, 10, orders.StatusDelivered, orders.ReasonNone)
	f.learn(a, t, old)
	f.learn(a, t, recent)
	if n, err := f.svc.Purge(t.Context()); err != nil || n < 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	r, err := f.svc.ForOrder(a, recent.ID)
	if err != nil || r.History.Orders != 1 || r.History.Outcomes[domain.Returned] != 0 {
		t.Fatalf("after the purge: %+v, %v", r, err)
	}
}
