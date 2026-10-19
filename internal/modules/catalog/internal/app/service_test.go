package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type fixedCurrency money.Currency

func (c fixedCurrency) StoreCurrency(context.Context) (money.Currency, error) {
	return money.Currency(c), nil
}

// recorder counts audit actions.
type recorder struct {
	mu      sync.Mutex
	actions []string
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, e.Action)
	return nil
}

func (r *recorder) has(action string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.actions, action)
}

type fixture struct {
	objects *memStore
	ins     *river.Client[pgx.Tx]
	svc     *app.Service
	pool    *pgxpool.Pool
	qc      *testkit.QueryCounter
	audit   *recorder
	ctxA    context.Context
	ctxB    context.Context
	ids     *ulid.Generator
}

// newFixture creates two stores (A in EGP, B in SAR) and a service whose
// pool counts queries and allows enough connections for real contention.
func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, qc := testkit.CountingPool(t, infra.AppURL+"&pool_max_conns=24")
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	rec := &recorder{}
	objects := newMemStore()
	f := fixture{pool: pool, qc: qc, audit: rec, ids: ids, objects: objects, ins: ins,
		svc: app.NewService(pool, ins, ids, rec, fixedCurrency(money.EGP), app.Media{Objects: objects, Limits: testLimits})}
	f.ctxA = createStore(t, pool, ids, "EGP")
	f.ctxB = createStore(t, pool, ids, "SAR")
	return f
}

func createStore(t *testing.T, pool *pgxpool.Pool, ids *ulid.Generator, currency string) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', $3, 'ar', 'Africa/Cairo')`,
			id.ID, "cat-"+strings.ToLower(id.String()[14:]), currency)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func ptr[T any](v T) *T { return &v }

func tee(title string, onHand int) domain.ProductInput {
	return domain.ProductInput{
		Title: title, Status: "active",
		Options: []domain.Option{{Name: "Size", Values: []string{"S", "M", "L"}}},
		Variants: []domain.VariantInput{
			{SKU: title + "-S", OptionValues: []string{"S"}, PriceMinor: 15000, OnHand: onHand},
			{SKU: title + "-M", OptionValues: []string{"M"}, PriceMinor: 16000, CompareAtMinor: ptr(int64(20000)), OnHand: onHand},
		},
	}
}

func single(title string, onHand int, tracked bool) domain.ProductInput {
	return domain.ProductInput{Title: title, Status: "active",
		Variants: []domain.VariantInput{{PriceMinor: 5000, OnHand: onHand, Tracked: &tracked}}}
}

func mustCreate(ctx context.Context, t *testing.T, f fixture, in domain.ProductInput) domain.Product {
	t.Helper()
	p, err := f.svc.CreateProduct(ctx, in)
	if err != nil {
		t.Fatalf("create %q: %v", in.Title, err)
	}
	return p
}

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if !errs.Is(err, k) {
		t.Fatalf("want %v error, got %v", k, err)
	}
}

func wantField(t *testing.T, err error, field, code string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("want validation error, got %v", err)
	}
	for _, fe := range e.Fields {
		if fe.Field == field && fe.Code == code {
			return
		}
	}
	t.Fatalf("want field %s:%s, got %+v", field, code, e.Fields)
}

func TestProductLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cat, err := f.svc.CreateCategory(f.ctxA, domain.CategoryInput{Name: "Shirts"})
	if err != nil {
		t.Fatal(err)
	}
	in := tee("Basic Tee", 5)
	in.CategoryIDs = []ulid.ID{cat.ID}
	p := mustCreate(f.ctxA, t, f, in)
	if p.Slug != "basic-tee" || len(p.Variants) != 2 || !slices.Equal(p.CategoryIDs, []ulid.ID{cat.ID}) {
		t.Fatalf("created %+v", p)
	}
	v0 := p.Variants[0]
	if v0.Price.Currency() != money.EGP || v0.Price.Amount() != 15000 || v0.Stock != (domain.Stock{Tracked: true, OnHand: 5}) {
		t.Fatalf("variant %+v", v0)
	}
	if c := p.Variants[1].CompareAt; c == nil || c.Amount() != 20000 {
		t.Fatalf("compare-at %+v", c)
	}
	if !f.audit.has("product.created") {
		t.Fatal("create not audited")
	}

	got, err := f.svc.GetProduct(f.ctxA, p.ID)
	if err != nil || got.Title != "Basic Tee" {
		t.Fatalf("get: %v %+v", err, got)
	}

	// Keep S (new price), drop M, add L; drop the category.
	upd := tee("Basic Tee", 0)
	upd.Slug = "tee"
	upd.Variants = []domain.VariantInput{
		{ID: &v0.ID, SKU: "Basic Tee-S", OptionValues: []string{"S"}, PriceMinor: 14000},
		{SKU: "Basic Tee-M", OptionValues: []string{"L"}, PriceMinor: 17000, OnHand: 2}, // reuses M's SKU
	}
	p2, err := f.svc.UpdateProduct(f.ctxA, p.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Slug != "tee" || len(p2.Variants) != 2 || len(p2.CategoryIDs) != 0 {
		t.Fatalf("updated %+v", p2)
	}
	if p2.Variants[0].ID != v0.ID || p2.Variants[0].Price.Amount() != 14000 || p2.Variants[0].Stock.OnHand != 5 {
		t.Fatalf("kept variant lost identity or stock: %+v", p2.Variants[0])
	}
	if p2.Variants[1].OptionValues[0] != "L" || p2.Variants[1].Stock.OnHand != 2 {
		t.Fatalf("new variant %+v", p2.Variants[1])
	}

	// A variant ID from another product is rejected.
	other := mustCreate(f.ctxA, t, f, single("Other", 1, true))
	bad := upd
	bad.Variants = []domain.VariantInput{{ID: &other.Variants[0].ID, OptionValues: []string{"S"}}}
	_, err = f.svc.UpdateProduct(f.ctxA, p.ID, bad)
	wantField(t, err, "variants[0].id", "unknown")
	_, err = f.svc.CreateProduct(f.ctxA, bad)
	wantField(t, err, "variants[0].id", "not_allowed")

	if err = f.svc.DeleteProduct(f.ctxA, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.GetProduct(f.ctxA, p.ID)
	wantKind(t, err, errs.KindNotFound)
	wantKind(t, f.svc.DeleteProduct(f.ctxA, p.ID), errs.KindNotFound)
	_, err = f.svc.UpdateProduct(f.ctxA, p.ID, upd)
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.GetSellableVariant(f.ctxA, v0.ID)
	wantKind(t, err, errs.KindNotFound)

	// Deleting freed the slug and the SKUs.
	upd.Variants[0].ID = nil
	again := mustCreate(f.ctxA, t, f, upd)
	if again.Slug != "tee" {
		t.Fatalf("slug not reusable: %q", again.Slug)
	}
}

func TestSlugAndSKUUniquePerStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mustCreate(f.ctxA, t, f, tee("Hoodie", 1))
	in := tee("Hoodie", 1)
	in.Variants[0].SKU, in.Variants[1].SKU = "x1", "x2"
	_, err := f.svc.CreateProduct(f.ctxA, in)
	wantField(t, err, "slug", "taken")

	in = tee("Hoodie 2", 1)
	in.Variants[0].SKU = "hoodie-s" // case-insensitive clash with "Hoodie-S"
	_, err = f.svc.CreateProduct(f.ctxA, in)
	wantField(t, err, "variants", "sku_taken")

	// Store B may use the same slug and SKUs.
	b := mustCreate(f.ctxB, t, f, tee("Hoodie", 1))
	if b.Slug != "hoodie" || b.Variants[0].Price.Currency() != money.EGP {
		t.Fatalf("store B product %+v", b)
	}
}

func TestListProductsConstantQueries(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cat, err := f.svc.CreateCategory(f.ctxA, domain.CategoryInput{Name: "All"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(i int) domain.Product {
		in := tee(fmt.Sprintf("P%02d", i), i)
		in.CategoryIDs = []ulid.ID{cat.ID}
		if i%2 == 1 {
			in.Status = "draft"
		}
		return mustCreate(f.ctxA, t, f, in)
	}
	first := mk(0)

	count := func(q app.ProductQuery) (app.ProductPage, int64) {
		t.Helper()
		f.qc.Reset()
		page, listErr := f.svc.ListProducts(f.ctxA, q)
		if listErr != nil {
			t.Fatal(listErr)
		}
		return page, f.qc.Count()
	}
	page, one := count(app.ProductQuery{})
	if len(page.Items) != 1 || page.Items[0].ID != first.ID || page.NextCursor != "" {
		t.Fatalf("page %+v", page)
	}
	for i := 1; i < 30; i++ {
		mk(i)
	}
	page, many := count(app.ProductQuery{Limit: 25})
	if len(page.Items) != 25 || page.NextCursor == "" {
		t.Fatalf("got %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	// BEGIN, set tenant, products (with category IDs), variants, images, COMMIT.
	if one != 6 || many != one {
		t.Fatalf("queries: 1 product = %d, 25 products = %d; want 6 for both", one, many)
	}
	for _, p := range page.Items {
		if len(p.Variants) != 2 || len(p.CategoryIDs) != 1 {
			t.Fatalf("product %s not fully loaded: %d variants, %d categories", p.Title, len(p.Variants), len(p.CategoryIDs))
		}
	}
	if page.Items[0].Title != "P29" {
		t.Fatalf("not newest first: %s", page.Items[0].Title)
	}
	rest, _ := count(app.ProductQuery{Limit: 25, Cursor: page.NextCursor})
	if len(rest.Items) != 5 || rest.NextCursor != "" || rest.Items[4].ID != first.ID {
		t.Fatalf("second page: %d items, cursor %q", len(rest.Items), rest.NextCursor)
	}

	drafts, _ := count(app.ProductQuery{Status: "draft", Limit: 100})
	if len(drafts.Items) != 15 {
		t.Fatalf("drafts = %d, want 15", len(drafts.Items))
	}
	inCat, n := count(app.ProductQuery{CategoryID: &cat.ID, Limit: 100})
	if len(inCat.Items) != 30 || n != one {
		t.Fatalf("category filter: %d items, %d queries", len(inCat.Items), n)
	}
	empty, err := f.svc.ListProducts(f.ctxB, app.ProductQuery{})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("store B sees A's products: %v %d", err, len(empty.Items))
	}

	_, err = f.svc.ListProducts(f.ctxA, app.ProductQuery{Status: "gone"})
	wantField(t, err, "status", "invalid")
	_, err = f.svc.ListProducts(f.ctxA, app.ProductQuery{Cursor: "nope"})
	wantField(t, err, "cursor", "invalid")
}

// TestReserveStockConcurrent is the P2 acceptance test: 50 concurrent
// reservations of one unit against a stock of 10 on real PostgreSQL;
// exactly 10 succeed.
func TestReserveStockConcurrent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := mustCreate(f.ctxA, t, f, single("Limited", 10, true)).Variants[0]

	const callers = 50
	var ok, short atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errc := make(chan error, callers)
	for i := range callers {
		wg.Go(func() {
			<-start
			_, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{
				Ref: fmt.Sprintf("checkout-%d", i), Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 1}},
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, app.ErrInsufficientStock):
				short.Add(1)
			default:
				errc <- err
			}
		})
	}
	close(start)
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Errorf("unexpected error: %v", err)
	}
	if ok.Load() != 10 || short.Load() != callers-10 {
		t.Fatalf("succeeded = %d, short = %d; want 10 and %d", ok.Load(), short.Load(), callers-10)
	}
	sv, err := f.svc.GetSellableVariant(f.ctxA, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Stock.Reserved != 10 || sv.Stock.OnHand != 10 || sv.Stock.Available() != 0 {
		t.Fatalf("stock after the rush: %+v", sv.Stock)
	}
}

func TestReserveMultiLineConcurrentNoDeadlock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := mustCreate(f.ctxA, t, f, single("A", 20, true)).Variants[0]
	b := mustCreate(f.ctxA, t, f, single("B", 20, true)).Variants[0]
	var wg sync.WaitGroup
	errc := make(chan error, 40)
	for i := range 40 {
		wg.Go(func() {
			lines := []domain.ReserveLine{{VariantID: a.ID, Qty: 1}, {VariantID: b.ID, Qty: 1}}
			if i%2 == 1 {
				lines[0], lines[1] = lines[1], lines[0] // opposite order on input
			}
			_, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: fmt.Sprintf("m-%d", i), Lines: lines})
			if err != nil && !errors.Is(err, app.ErrInsufficientStock) {
				errc <- err
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Errorf("unexpected error (deadlock?): %v", err)
	}
	for _, id := range []ulid.ID{a.ID, b.ID} {
		sv, err := f.svc.GetSellableVariant(f.ctxA, id)
		if err != nil || sv.Stock.Reserved != 20 {
			t.Fatalf("reserved = %d (%v), want 20", sv.Stock.Reserved, err)
		}
	}
}

func TestReserveIdempotencyAndRelease(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := mustCreate(f.ctxA, t, f, single("A", 5, true)).Variants[0]
	b := mustCreate(f.ctxA, t, f, single("B", 1, true)).Variants[0]
	req := domain.ReserveRequest{Ref: "order-1", Lines: []domain.ReserveLine{{VariantID: a.ID, Qty: 2}}}
	r1, err := f.svc.ReserveStock(f.ctxA, req)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != domain.ReservationActive || time.Until(r1.ExpiresAt) < 14*time.Minute {
		t.Fatalf("reservation %+v", r1)
	}
	r2, err := f.svc.ReserveStock(f.ctxA, req)
	if err != nil || !r2.ExpiresAt.Equal(r1.ExpiresAt) {
		t.Fatalf("retry: %v %+v", err, r2)
	}
	reserved(t, f, a.ID, 2) // the retry did not reserve twice

	_, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "order-1", Lines: []domain.ReserveLine{{VariantID: a.ID, Qty: 3}}})
	if !errors.Is(err, app.ErrReservationRefUsed) {
		t.Fatalf("different lines under one ref: %v", err)
	}

	// All or nothing: B is short, so A is not held either.
	_, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "order-2",
		Lines: []domain.ReserveLine{{VariantID: a.ID, Qty: 1}, {VariantID: b.ID, Qty: 2}}})
	var se *app.StockError
	if !errors.As(err, &se) || se.VariantID != b.ID || !errors.Is(err, app.ErrInsufficientStock) {
		t.Fatalf("short line: %v", err)
	}
	reserved(t, f, a.ID, 2)

	if err = f.svc.ReleaseStock(f.ctxA, "order-1"); err != nil {
		t.Fatal(err)
	}
	reserved(t, f, a.ID, 0)
	if err = f.svc.ReleaseStock(f.ctxA, "order-1"); err != nil {
		t.Fatalf("second release: %v", err)
	}
	reserved(t, f, a.ID, 0)
	if err = f.svc.ReleaseStock(f.ctxA, "never"); !errors.Is(err, app.ErrReservationNotFound) {
		t.Fatalf("unknown ref: %v", err)
	}
	wantKind(t, f.svc.ReleaseStock(f.ctxA, "bad ref"), errs.KindValidation)
	_, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "x"})
	wantKind(t, err, errs.KindValidation)
}

func reserved(t *testing.T, f fixture, id ulid.ID, want int) {
	t.Helper()
	sv, err := f.svc.GetSellableVariant(f.ctxA, id)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Stock.Reserved != want {
		t.Fatalf("reserved = %d, want %d", sv.Stock.Reserved, want)
	}
}

func TestReserveSellability(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	draft := single("Draft", 5, true)
	draft.Status = "draft"
	dv := mustCreate(f.ctxA, t, f, draft).Variants[0]
	_, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "d", Lines: []domain.ReserveLine{{VariantID: dv.ID, Qty: 1}}})
	if !errors.Is(err, app.ErrVariantNotSellable) {
		t.Fatalf("draft product: %v", err)
	}
	_, err = f.svc.GetSellableVariant(f.ctxA, dv.ID)
	wantKind(t, err, errs.KindNotFound)

	// Untracked stock never runs out and is not counted.
	free := mustCreate(f.ctxA, t, f, single("Digital", 0, false)).Variants[0]
	if _, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "u", Lines: []domain.ReserveLine{{VariantID: free.ID, Qty: 500}}}); err != nil {
		t.Fatal(err)
	}
	reserved(t, f, free.ID, 0)
	if err = f.svc.ReleaseStock(f.ctxA, "u"); err != nil {
		t.Fatal(err)
	}

	sv, err := f.svc.GetSellableVariant(f.ctxA, free.ID)
	if err != nil || sv.ProductTitle != "Digital" || sv.Price.Amount() != 5000 || sv.Stock.Tracked || !sv.Stock.CanSell(1_000_000) {
		t.Fatalf("sellable %+v %v", sv, err)
	}
}

func TestReservationExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := mustCreate(f.ctxA, t, f, single("Expiring", 3, true)).Variants[0]
	worker := &app.ExpireWorker{DB: f.pool}
	run := func(ref string) error {
		args := app.ExpireReservationArgs{TenantArgs: jobs.TenantArgs{StoreID: mustStore(f.ctxA, t)}, Ref: ref}
		return worker.Work(f.ctxA, &river.Job[app.ExpireReservationArgs]{Args: args})
	}

	// Not expired yet: the job snoozes and keeps the stock held.
	if _, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "live", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := run("live"); err == nil {
		t.Fatal("early job did not snooze")
	}
	reserved(t, f, v.ID, 1)
	var scheduled int
	if err := postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctxA, `SELECT count(*) FROM river_job WHERE kind = 'catalog.expire_reservation' AND args->>'ref' = 'live'
			AND scheduled_at > now() + interval '14 minutes'`).Scan(&scheduled)
	}); err != nil {
		t.Fatal(err)
	}
	if scheduled != 1 {
		t.Fatalf("expiry jobs scheduled = %d, want 1", scheduled)
	}

	// Expired: the job releases it, once.
	f.svc.SetClock(func() time.Time { return time.Now().Add(-time.Hour) })
	if _, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "old", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 2}}}); err != nil {
		t.Fatal(err)
	}
	reserved(t, f, v.ID, 3)
	for range 2 {
		if err := run("old"); err != nil {
			t.Fatal(err)
		}
	}
	reserved(t, f, v.ID, 1)
	if err := run("missing"); err != nil {
		t.Fatalf("missing ref: %v", err)
	}
	// Releasing an expired ref is a no-op.
	if err := f.svc.ReleaseStock(f.ctxA, "old"); err != nil {
		t.Fatal(err)
	}
	reserved(t, f, v.ID, 1)
}

func mustStore(ctx context.Context, t *testing.T) tenant.StoreID {
	t.Helper()
	id, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSetStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := mustCreate(f.ctxA, t, f, single("Stocked", 5, true)).Variants[0]
	if _, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "r", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 3}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetStock(f.ctxA, v.ID, domain.StockInput{Tracked: true, OnHand: 2}); !errors.Is(err, store.ErrBelowReserved) {
		t.Fatalf("below reserved: %v", err)
	}
	st, err := f.svc.SetStock(f.ctxA, v.ID, domain.StockInput{Tracked: true, OnHand: 40})
	if err != nil || st != (domain.Stock{Tracked: true, OnHand: 40, Reserved: 3}) {
		t.Fatalf("set: %+v %v", st, err)
	}
	if !f.audit.has("inventory.updated") {
		t.Fatal("not audited")
	}
	_, err = f.svc.SetStock(f.ctxA, v.ID, domain.StockInput{OnHand: -1})
	wantKind(t, err, errs.KindValidation)
	_, err = f.svc.SetStock(f.ctxA, f.ids.New(), domain.StockInput{OnHand: 1})
	wantKind(t, err, errs.KindNotFound)
}

func TestCategories(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mk := func(name string, parent *ulid.ID) (domain.Category, error) {
		return f.svc.CreateCategory(f.ctxA, domain.CategoryInput{Name: name, ParentID: parent})
	}
	root, err := mk("ملابس", nil)
	if err != nil {
		t.Fatal(err)
	}
	child, err := mk("رجالي", &root.ID)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := mk("قمصان", &child.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mk("Too deep", &leaf.ID)
	wantField(t, err, "parent_id", "too_deep")
	_, err = mk("Orphan", ptr(f.ids.New()))
	wantField(t, err, "parent_id", "unknown")
	_, err = mk("ملابس", nil)
	wantField(t, err, "slug", "taken")

	// Moving root under its own grandchild is a cycle.
	_, err = f.svc.UpdateCategory(f.ctxA, root.ID, domain.CategoryInput{Name: "ملابس", ParentID: &leaf.ID})
	wantField(t, err, "parent_id", "cycle")
	// Moving a 2-level subtree under a new root at depth 2 would make 4.
	other, err := mk("Other", nil)
	if err != nil {
		t.Fatal(err)
	}
	mid, err := mk("Mid", &other.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateCategory(f.ctxA, child.ID, domain.CategoryInput{Name: "رجالي", ParentID: &mid.ID})
	wantField(t, err, "parent_id", "too_deep")
	moved, err := f.svc.UpdateCategory(f.ctxA, child.ID, domain.CategoryInput{Name: "Men", Slug: "men", ParentID: &other.ID, Position: 3})
	if err != nil || moved.Slug != "men" || *moved.ParentID != other.ID || moved.Position != 3 {
		t.Fatalf("move: %v %+v", err, moved)
	}
	_, err = f.svc.UpdateCategory(f.ctxA, f.ids.New(), domain.CategoryInput{Name: "x"})
	wantKind(t, err, errs.KindNotFound)

	all, err := f.svc.ListCategories(f.ctxA)
	if err != nil || len(all) != 5 || all[0].ParentID != nil {
		t.Fatalf("list: %v %d", err, len(all))
	}

	// Delete: a parent with children is refused; products leave a deleted category.
	if err = f.svc.DeleteCategory(f.ctxA, other.ID); !errors.Is(err, store.ErrCategoryHasChildren) {
		t.Fatalf("delete parent: %v", err)
	}
	in := single("In leaf", 1, true)
	in.CategoryIDs = []ulid.ID{leaf.ID}
	p := mustCreate(f.ctxA, t, f, in)
	if err = f.svc.DeleteCategory(f.ctxA, leaf.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetProduct(f.ctxA, p.ID); len(got.CategoryIDs) != 0 {
		t.Fatalf("product still in deleted category: %v", got.CategoryIDs)
	}
	wantKind(t, f.svc.DeleteCategory(f.ctxA, leaf.ID), errs.KindNotFound)
	if !f.audit.has("category.deleted") {
		t.Fatal("not audited")
	}

	// Unknown and foreign categories are rejected by the composite FK.
	bCat, err := f.svc.CreateCategory(f.ctxB, domain.CategoryInput{Name: "B only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ulid.ID{f.ids.New(), bCat.ID} {
		in := single("Bad cat", 1, true)
		in.CategoryIDs = []ulid.ID{id}
		_, err = f.svc.CreateProduct(f.ctxA, in)
		wantField(t, err, "category_ids", "unknown")
	}
	_, err = f.svc.CreateCategory(f.ctxA, domain.CategoryInput{Name: "x", ParentID: &bCat.ID})
	wantField(t, err, "parent_id", "unknown")
}

// TestCrossTenant: store A can neither see nor touch store B's catalog.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pb := mustCreate(f.ctxB, t, f, single("B secret", 5, true))
	vb := pb.Variants[0]
	cb, err := f.svc.CreateCategory(f.ctxB, domain.CategoryInput{Name: "B cat"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.svc.GetProduct(f.ctxA, pb.ID)
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.UpdateProduct(f.ctxA, pb.ID, single("hijack", 1, true))
	wantKind(t, err, errs.KindNotFound)
	wantKind(t, f.svc.DeleteProduct(f.ctxA, pb.ID), errs.KindNotFound)
	_, err = f.svc.GetSellableVariant(f.ctxA, vb.ID)
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.SetStock(f.ctxA, vb.ID, domain.StockInput{Tracked: true, OnHand: 0})
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "steal", Lines: []domain.ReserveLine{{VariantID: vb.ID, Qty: 5}}})
	if !errors.Is(err, app.ErrVariantNotSellable) {
		t.Fatalf("reserve B's variant from A: %v", err)
	}
	_, err = f.svc.UpdateCategory(f.ctxA, cb.ID, domain.CategoryInput{Name: "x"})
	wantKind(t, err, errs.KindNotFound)
	wantKind(t, f.svc.DeleteCategory(f.ctxA, cb.ID), errs.KindNotFound)

	// B's data is intact.
	sv, err := f.svc.GetSellableVariant(f.ctxB, vb.ID)
	if err != nil || sv.Stock != (domain.Stock{Tracked: true, OnHand: 5}) || sv.ProductTitle != "B secret" {
		t.Fatalf("B's variant changed: %+v %v", sv, err)
	}
	if cats, _ := f.svc.ListCategories(f.ctxB); len(cats) != 1 || cats[0].Name != "B cat" {
		t.Fatalf("B's categories changed: %+v", cats)
	}
}

func TestNoTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.ListProducts(ctx, app.ProductQuery{}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("list without tenant: %v", err)
	}
	if _, err := f.svc.CreateProduct(ctx, single("x", 1, true)); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("create without tenant: %v", err)
	}
}
