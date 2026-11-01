package app_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// fakeOrders is orders.Outcomes in memory, per store.
type fakeOrders struct {
	mu    sync.Mutex
	m     map[tenant.StoreID][]orders.Order
	gets  int // Get calls
	pages int // BeforeWithLines calls
}

func (f *fakeOrders) put(ctx context.Context, o orders.Order) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		s = tenant.StoreID{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.m[s] {
		if f.m[s][i].ID == o.ID {
			f.m[s][i] = o
			return
		}
	}
	f.m[s] = append(f.m[s], o)
}

func (f *fakeOrders) Get(ctx context.Context, id ulid.ID) (orders.Order, error) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		return orders.Order{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	for _, o := range f.m[s] {
		if o.ID == id {
			return o, nil
		}
	}
	return orders.Order{}, orders.ErrOrderNotFound
}

func (f *fakeOrders) Before(ctx context.Context, before ulid.ID, limit int) ([]orders.Order, error) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []orders.Order
	for _, o := range slices.Backward(f.m[s]) {
		if (before.IsZero() || bytes.Compare(o.ID[:], before[:]) < 0) && len(out) < limit {
			out = append(out, o)
		}
	}
	return out, nil
}

// BeforeWithLines implements orders.Outcomes: its orders are whole.
func (f *fakeOrders) BeforeWithLines(ctx context.Context, before ulid.ID, limit int) ([]orders.Order, error) {
	f.mu.Lock()
	f.pages++
	f.mu.Unlock()
	return f.Before(ctx, before, limit)
}

// fakeCharges is shipping.Charges with set answers.
type fakeCharges struct {
	mu sync.Mutex
	m  map[ulid.ID]money.Money
}

func (f *fakeCharges) CarrierCharges(_ context.Context, ids []ulid.ID) (map[ulid.ID]money.Money, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[ulid.ID]money.Money{}
	for _, id := range ids {
		if c, ok := f.m[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

var cairo = mustLoc("Africa/Cairo") //nolint:gochecknoglobals // the stores' zone

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err) //nolint:forbidigo // a test's fixed zone
	}
	return l
}

type fixture struct {
	svc     *app.Service
	pool    *pgxpool.Pool
	ids     *ulid.Generator
	orders  *fakeOrders
	charges *fakeCharges
	jobs    *testkit.Jobs
	now     time.Time
	// The two products every order of the fixture has.
	watch, strap ulid.ID
}

// newFixture: primary and read pools are separate pools on the same
// database, as without a replica (ADR-0064).
func newFixture(t *testing.T, primary *pgxpool.Pool, read *postgres.ReadPool) *fixture {
	t.Helper()
	f := &fixture{pool: primary, ids: ulid.NewGenerator(), orders: &fakeOrders{m: map[tenant.StoreID][]orders.Order{}},
		charges: &fakeCharges{m: map[ulid.ID]money.Money{}}, jobs: testkit.NewJobs(), now: time.Now()}
	f.watch, f.strap = f.ids.New(), f.ids.New()
	f.svc = app.NewService(app.Deps{DB: primary, Read: read, Orders: f.orders, Charges: f.charges, Jobs: f.jobs, IDs: f.ids,
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: "EGP", Location: cairo}, nil
		},
		Now: func() time.Time { return f.now }})
	return f
}

func newDefault(t *testing.T) *fixture {
	t.Helper()
	primary, read := pools(t)
	return newFixture(t, primary, read)
}

func pools(t *testing.T) (*pgxpool.Pool, *postgres.ReadPool) {
	t.Helper()
	primary, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(primary.Close)
	read, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	return primary, postgres.ReadOnly(read)
}

func (f *fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "anl-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func egp(t *testing.T, n int64) money.Money {
	t.Helper()
	m, err := money.New(n, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// order puts an order of 300 (a watch costing 120) + 150 (a strap of
// unknown cost), 50 delivery and a 15 COD fee, placed at, now in status.
func (f *fixture) order(ctx context.Context, t *testing.T, at time.Time, st orders.Status, campaign, governorate string) orders.Order {
	t.Helper()
	cost := egp(t, 12000)
	watch, strap := f.watch, f.strap
	o := orders.Order{ID: f.ids.New(), Status: st, PaymentMethod: orders.PaymentCOD, PaymentStatus: orders.PaymentUnpaid,
		Subtotal: egp(t, 45000), Discount: egp(t, 0), Shipping: egp(t, 5000), CODFee: egp(t, 1500), Total: egp(t, 51500),
		CreatedAt: at, StatusChangedAt: at, Attribution: orders.Attribution{Campaign: campaign},
		Lines: []orders.Line{
			{VariantID: f.ids.New(), ProductID: watch, Title: "ساعة", Qty: 1, UnitPrice: egp(t, 30000), Total: egp(t, 30000), UnitCost: &cost},
			{VariantID: f.ids.New(), ProductID: strap, Title: "سوار", Qty: 1, UnitPrice: egp(t, 15000), Total: egp(t, 15000)},
		}}
	o.Contact.Governorate = governorate
	o.Contact.Phone = phone.E164{}
	f.orders.put(ctx, o)
	if err := f.svc.Learn(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	return o
}

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err) //nolint:forbidigo // a test's fixed day
	}
	return d
}

func (f *fixture) rollup(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, err := f.svc.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestProfit (ADR-0069): orders' facts become daily figures, read back
// by dimension; ad spend lands on its campaign; settings change every
// day's figures; another store sees none of it.
func TestProfit(t *testing.T) {
	t.Parallel()
	f := newDefault(t)
	a, b := f.store(t), f.store(t)
	// 22:30 UTC on the 30th is past midnight in Cairo: the store's day
	// (the 31st) counts.
	late := time.Date(2026, 10, 30, 22, 30, 0, 0, time.UTC)
	f.order(a, t, late, orders.StatusDelivered, "eid", "EG-C")
	f.order(a, t, late.Add(time.Hour), orders.StatusReturned, "eid", "EG-ALX")
	f.order(a, t, late.Add(2*time.Hour), orders.StatusCancelled, "", "EG-C")
	f.order(a, t, late.Add(24*time.Hour), orders.StatusShipped, "", "EG-C")
	f.order(b, t, late, orders.StatusDelivered, "secret-b", "EG-GZ")
	spend, err := f.svc.AddAdSpend(a, app.AdSpendInput{Day: day("2026-10-31"), Platform: "meta", Campaign: "eid", AmountMinor: 20000})
	if err != nil {
		t.Fatal(err)
	}
	f.rollup(a, t)
	f.rollup(b, t)

	r, err := f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByCampaign)
	if err != nil {
		t.Fatal(err)
	}
	delivered := domain.OfOrder(domain.Order{State: domain.Delivered, Revenue: 45000, Shipping: 5000, CODFee: 1500, OnDelivery: 51500,
		Lines: []domain.Line{{Cost: ptr(12000)}, {}}}, domain.Settings{})
	returned := domain.OfOrder(domain.Order{State: domain.Returned, Shipping: 5000}, domain.Settings{})
	want := delivered.Profit() + returned.Profit() - 20000
	if r.Totals.Orders != 4 || r.Totals.Delivered != 1 || r.Totals.Returned != 1 || r.Totals.Open != 1 || r.Totals.Cancelled != 1 ||
		r.Totals.CostMissing != 1 || r.Totals.AdSpend != 20000 || r.Totals.Profit() != want {
		t.Fatalf("totals = %+v (profit %d), want profit %d", r.Totals, r.Totals.Profit(), want)
	}
	if len(r.Days) != 2 || !r.Days[0].Day.Equal(day("2026-10-31")) || r.Days[0].Orders != 3 {
		t.Fatalf("days = %+v (the late orders are the 31st, Cairo time)", r.Days)
	}
	var eid domain.Row
	for _, row := range r.Rows {
		if row.Key == "eid" {
			eid = row
		}
	}
	if eid.Orders != 2 || eid.AdSpend != 20000 || eid.Profit() != delivered.Profit()+returned.Profit()-20000 {
		t.Fatalf("eid = %+v", eid)
	}
	if r, err = f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByProduct); err != nil || len(r.Rows) != 2 ||
		r.Rows[0].Label != "ساعة" || r.Rows[0].ProductCost != 12000 {
		t.Fatalf("by product = %+v, %v", r.Rows, err)
	}
	if r, err = f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByGovernorate); err != nil || len(r.Rows) != 2 {
		t.Fatalf("by governorate = %+v, %v", r.Rows, err)
	}

	// The store's own estimates: delivery 40, return 30, COD fee 1%.
	if _, err = f.svc.SaveSettings(a, domain.Settings{DeliveryCost: ptr(4000), ReturnCost: ptr(3000), CODFeeBP: 100}); err != nil {
		t.Fatal(err)
	}
	f.rollup(a, t)
	s := domain.Settings{DeliveryCost: ptr(4000), ReturnCost: ptr(3000), CODFeeBP: 100}
	want = domain.OfOrder(domain.Order{State: domain.Delivered, Revenue: 45000, Shipping: 5000, CODFee: 1500, OnDelivery: 51500,
		Lines: []domain.Line{{Cost: ptr(12000)}, {}}}, s).Profit() + domain.OfOrder(domain.Order{State: domain.Returned, Shipping: 5000}, s).Profit() - 20000
	if r, err = f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByCampaign); err != nil || r.Totals.Profit() != want {
		t.Fatalf("after the estimates: profit %d, want %d (%v)", r.Totals.Profit(), want, err)
	}

	// The ad spend goes.
	if err = f.svc.DeleteAdSpend(a, spend.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DeleteAdSpend(b, spend.ID); err == nil {
		t.Fatal("store B deleted A's spend")
	}
	f.rollup(a, t)
	if r, err = f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByCampaign); err != nil || r.Totals.AdSpend != 0 {
		t.Fatalf("after deleting the spend: %+v, %v", r.Totals, err)
	}

	// Store B sees only its own.
	rb, err := f.svc.Profit(b, day("2026-10-01"), day("2026-11-03"), domain.ByCampaign)
	if err != nil || rb.Totals.Orders != 1 || len(rb.Rows) != 1 || rb.Rows[0].Key != "secret-b" {
		t.Fatalf("store B = %+v, %v", rb, err)
	}
	if ra, _ := f.svc.Profit(a, day("2026-10-01"), day("2026-11-03"), domain.ByCampaign); slices.ContainsFunc(ra.Rows, func(r domain.Row) bool { return r.Key == "secret-b" }) {
		t.Fatal("store A sees B's campaign")
	}
}

func ptr(v int64) *int64 { return &v }

// TestProfitReadsTheReadPoolOnly is P8's acceptance: the dashboard's
// queries run on the read pool, none on the primary.
func TestProfitReadsTheReadPoolOnly(t *testing.T) {
	t.Parallel()
	primary, primaryQueries := testkit.CapturingPool(t, infra.AppURL)
	readPool, readQueries := testkit.CapturingPool(t, infra.AppURL)
	f := newFixture(t, primary, postgres.ReadOnly(readPool))
	a := f.store(t)
	f.order(a, t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), orders.StatusDelivered, "eid", "EG-C")
	f.rollup(a, t)
	before := primaryQueries.Count("profit_daily")
	if _, err := f.svc.Profit(a, day("2026-10-01"), day("2026-11-01"), domain.ByProduct); err != nil {
		t.Fatal(err)
	}
	if n := primaryQueries.Count("profit_daily") - before; n != 0 {
		t.Fatalf("%d dashboard queries on the primary", n)
	}
	if n := readQueries.Count("FROM profit_daily"); n != 3 {
		t.Fatalf("%d dashboard queries on the read pool, want 3", n)
	}
	// Without a read pool, the dashboard is not served (never the primary).
	g := newFixture(t, primary, nil)
	if _, err := g.svc.Profit(a, day("2026-10-01"), day("2026-11-01"), domain.ByProduct); err == nil {
		t.Fatal("a dashboard without its read pool must fail")
	}
}

// TestSweep (ADR-0068): a carrier's real charge replaces the estimate.
func TestSweep(t *testing.T) {
	t.Parallel()
	f := newDefault(t)
	a := f.store(t)
	f.now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	o := f.order(a, t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), orders.StatusDelivered, "", "EG-C")
	f.order(a, t, time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC), orders.StatusDelivered, "", "EG-C")
	f.charges.m[o.ID] = egp(t, 7000)
	n, err := f.svc.Sweep(a)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	f.rollup(a, t)
	r, err := f.svc.Profit(a, day("2026-10-20"), day("2026-10-21"), domain.ByProduct)
	if err != nil || r.Totals.ShippingCost != 7000 || r.Totals.CODFees != 0 {
		t.Fatalf("after the carrier's charge: %+v, %v", r.Totals, err)
	}
	if n, err = f.svc.Sweep(a); err != nil || n != 0 {
		t.Fatalf("a second sweep found %d, %v", n, err)
	}
}

// TestBackfillAndRules: a store's past is learnt once; a moved order
// moves its figures; validation and the plan.
func TestBackfillAndRules(t *testing.T) {
	t.Parallel()
	f := newDefault(t)
	a := f.store(t)
	o := f.order(a, t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), orders.StatusShipped, "", "EG-C")
	if kinds := f.jobs.Kinds(); !slices.Contains(kinds, "analytics.backfill") {
		t.Fatalf("jobs = %v", kinds)
	}
	f.orders.mu.Lock()
	f.orders.gets, f.orders.pages = 0, 0
	f.orders.mu.Unlock()
	if n, err := f.svc.Backfill(a); err != nil || n != 1 {
		t.Fatalf("backfill = %d, %v", n, err)
	}
	// One read a page of orders with their lines, never one an order (P8
	// review M2).
	f.orders.mu.Lock()
	gets, pages := f.orders.gets, f.orders.pages
	f.orders.mu.Unlock()
	if gets != 0 || pages != 1 {
		t.Fatalf("backfill read %d orders one by one and %d pages", gets, pages)
	}
	f.rollup(a, t)
	r, err := f.svc.Profit(a, day("2026-10-20"), day("2026-10-21"), domain.ByProduct)
	if err != nil || r.Totals.Open != 1 || r.Totals.Profit() != 0 {
		t.Fatalf("shipped: %+v, %v", r.Totals, err)
	}
	o.Status = orders.StatusDelivered
	f.orders.put(a, o)
	if err = f.svc.Learn(a, o.ID); err != nil {
		t.Fatal(err)
	}
	f.rollup(a, t)
	if r, err = f.svc.Profit(a, day("2026-10-20"), day("2026-10-21"), domain.ByProduct); err != nil || r.Totals.Delivered != 1 || r.Totals.Open != 0 {
		t.Fatalf("delivered: %+v, %v", r.Totals, err)
	}
	if err = f.svc.Learn(a, f.ids.New()); err != nil {
		t.Fatalf("an order gone: %v", err)
	}

	for _, bad := range [][2]time.Time{{day("2026-10-02"), day("2026-10-01")}, {day("2025-01-01"), day("2026-10-01")}} {
		if _, err = f.svc.Profit(a, bad[0], bad[1], domain.ByProduct); err == nil {
			t.Errorf("range %v must fail", bad)
		}
	}
	for _, in := range []app.AdSpendInput{
		{Day: f.now.AddDate(0, 0, 2), Platform: "meta"},
		{Day: day("2026-10-20"), Platform: "myspace"},
		{Day: day("2026-10-20"), Platform: "meta", AmountMinor: -1},
	} {
		if _, err = f.svc.AddAdSpend(a, in); err == nil {
			t.Errorf("%+v must fail", in)
		}
	}
	if _, err = f.svc.SaveSettings(a, domain.Settings{CODFeeBP: 20000}); err == nil {
		t.Error("a fee over 100% must fail")
	}
	if got, sErr := f.svc.Settings(a); sErr != nil || got.DeliveryCost != nil || got.CODFeeBP != 0 {
		t.Errorf("settings before any = %+v, %v", got, sErr)
	}
	if _, err = f.svc.SaveSettings(a, domain.Settings{DeliveryCost: ptr(4500), CODFeeBP: 150}); err != nil {
		t.Fatal(err)
	}
	if got, sErr := f.svc.Settings(a); sErr != nil || got.DeliveryCost == nil || *got.DeliveryCost != 4500 || got.CODFeeBP != 150 {
		t.Errorf("settings = %+v, %v", got, sErr)
	}
	if _, err = f.svc.AdSpend(a, day("2026-10-02"), day("2026-10-01")); err == nil {
		t.Error("an empty range must fail")
	}
	if _, err = f.svc.ImportAdSpend(a, otherSource{}, day("2026-10-01"), day("2026-10-02")); err == nil {
		t.Error("an unknown platform must fail")
	}
	if _, err = f.svc.ImportAdSpend(a, adSource{}, day("2026-10-02"), day("2026-10-01")); err == nil {
		t.Error("an import's empty range must fail")
	}
}

// adSource is an ad platform importer in memory.
type adSource struct{ lines []app.AdSpendLine }

func (adSource) Platform() string { return "tiktok" }
func (s adSource) Spend(context.Context, time.Time, time.Time) ([]app.AdSpendLine, error) {
	return s.lines, nil
}

// otherSource names a platform analytics does not know.
type otherSource struct{ adSource }

func (otherSource) Platform() string { return "myspace" }

// TestImportAdSpend: an importer's spend replaces what it said before;
// manual entries stay.
func TestImportAdSpend(t *testing.T) {
	t.Parallel()
	f := newDefault(t)
	a := f.store(t)
	f.now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if _, err := f.svc.AddAdSpend(a, app.AdSpendInput{Day: day("2026-10-20"), Platform: "meta", AmountMinor: 1000}); err != nil {
		t.Fatal(err)
	}
	src := adSource{lines: []app.AdSpendLine{{Day: day("2026-10-20"), Campaign: "eid", AmountMinor: 5000}, {Day: day("2026-12-01"), AmountMinor: 9}}}
	for range 2 {
		if _, err := f.svc.ImportAdSpend(a, src, day("2026-10-01"), day("2026-11-01")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.svc.AdSpend(a, day("2026-10-01"), day("2026-11-01"))
	if err != nil || len(got) != 2 {
		t.Fatalf("spend = %+v, %v", got, err)
	}
	var total int64
	for _, s := range got {
		total += s.Amount
	}
	if total != 6000 {
		t.Fatalf("total spend %d, want 6000 (the import once, the manual entry)", total)
	}
}

// TestFactDepositAndCoupon: a prepaid delivery bought with a coupon. The
// coupon is spread over the lines to the last minor unit; the deposit
// kept (paid, or partly refunded) is what the customer paid upfront, and
// a fully refunded one is not (P8 review).
func TestFactDepositAndCoupon(t *testing.T) {
	t.Parallel()
	egp := func(v int64) money.Money { m, _ := money.New(v, money.EGP); return m } //nolint:errcheck // valid amounts
	ids := ulid.NewGenerator()
	cost := egp(10000)
	o := orders.Order{ID: ids.New(), Status: orders.StatusDelivered, PaymentMethod: orders.PaymentCOD, CreatedAt: time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC),
		Subtotal: egp(30000), Discount: egp(1000), Shipping: egp(5000), CODFee: egp(1500), Total: egp(35500), Deposit: egp(6500),
		Lines: []orders.Line{
			{ProductID: ids.New(), Title: "ساعة", Qty: 1, Total: egp(20000), UnitCost: &cost},
			{ProductID: ids.New(), Title: "سوار", Qty: 2, Total: egp(10000)},
		}}
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	for status, kept := range map[orders.PaymentStatus]int64{orders.PaymentPaid: 6500, orders.PaymentPartiallyRefunded: 6500, orders.PaymentRefunded: 0} {
		o.DepositStatus = status
		f, err := app.Fact(o, cairo)
		if err != nil {
			t.Fatal(err)
		}
		d := f.Order
		if d.DepositPaid != kept || d.OnDelivery != 35500-kept || d.Revenue != 29000 || d.Day != "2026-10-21" {
			t.Errorf("%s: kept %d, on delivery %d, revenue %d, day %s", status, d.DepositPaid, d.OnDelivery, d.Revenue, d.Day)
		}
		// 1000 of coupon: 666 on the watch, the rest (334) on the straps.
		if d.Lines[0].Revenue != 20000-666 || d.Lines[1].Revenue != 10000-334 || *d.Lines[0].Cost != 10000 || d.Lines[1].Cost != nil {
			t.Errorf("%s: lines %+v %+v", status, d.Lines[0], d.Lines[1])
		}
	}
}
