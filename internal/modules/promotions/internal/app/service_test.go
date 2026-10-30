package app_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

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

type fixture struct {
	svc  *app.Service
	pool *pgxpool.Pool
	ids  *ulid.Generator
	rec  *recorder
	now  time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := fixture{pool: pool, ids: ulid.NewGenerator(), rec: &recorder{}, now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: f.ids, Audit: f.rec, Now: func() time.Time { return f.now }})
	return f
}

func (f fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "prm-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func intp(n int) *int { return &n }

// redeem takes a code for a new order of customer, in its own
// transaction.
func (f fixture) redeem(ctx context.Context, t *testing.T, code string, subtotal int64, customer ulid.ID) (domain.Discount, ulid.ID, error) {
	t.Helper()
	order := f.ids.New()
	var d domain.Discount
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (err error) {
		d, err = f.svc.RedeemTx(ctx, tx, code, egp(t, subtotal), customer, order)
		return err
	})
	return d, order, err
}

func reason(err error) domain.Reason {
	var r *domain.Rejected
	if errors.As(err, &r) {
		return r.Reason
	}
	return ""
}

func TestManageCoupons(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	c, err := f.svc.Create(ctx, domain.Coupon{Code: " summer10 ", Kind: domain.Percent, Percent: 10, AmountMinor: 999, Active: true})
	if err != nil || c.Code != "SUMMER10" || c.AmountMinor != 0 || c.Uses != 0 || c.ID.IsZero() {
		t.Fatalf("create = %+v, %v", c, err)
	}
	if _, err = f.svc.Create(ctx, domain.Coupon{Code: "SUMMER10", Kind: domain.FreeShipping}); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("same code: %v", err)
	}
	if _, err = f.svc.Create(ctx, domain.Coupon{Code: "x", Kind: domain.Percent}); errs.KindOf(err) != errs.KindValidation {
		t.Fatalf("invalid: %v", err)
	}
	ship, err := f.svc.Create(ctx, domain.Coupon{Code: "SHIP", Kind: domain.FreeShipping, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.Coupons(ctx, "", 0)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != ship.ID || page.Next != "" {
		t.Fatalf("list = %+v, %v", page, err)
	}
	c.Code, c.Percent, c.Active = "AUTUMN15", 15, false
	if c, err = f.svc.Update(ctx, c.ID, c); err != nil || c.Code != "AUTUMN15" || c.Active {
		t.Fatalf("update = %+v, %v", c, err)
	}
	// Once an order holds it, its code is fixed and it cannot be deleted.
	if _, _, err = f.redeem(ctx, t, "SHIP", 1000, f.ids.New()); err != nil {
		t.Fatal(err)
	}
	ship.Code = "SHIP2"
	if _, err = f.svc.Update(ctx, ship.ID, ship); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("code of a used coupon: %v", err)
	}
	if err = f.svc.Delete(ctx, ship.ID); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("delete of a used coupon: %v", err)
	}
	if err = f.svc.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, e := range []error{
		func() error { _, e := f.svc.Coupon(ctx, c.ID); return e }(),
		f.svc.Delete(ctx, c.ID),
		func() error { _, e := f.svc.Update(ctx, f.ids.New(), ship); return e }(),
	} {
		if !errors.Is(e, app.ErrCouponNotFound) {
			t.Errorf("missing coupon: %v", e)
		}
	}
	if got := strings.Join(f.rec.actions, ","); got != "coupon.created,coupon.created,coupon.updated,coupon.deleted" {
		t.Fatalf("audit = %s", got)
	}
}

func TestPreviewAndRedeem(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.Create(ctx, domain.Coupon{Code: "ONCE", Kind: domain.Fixed, AmountMinor: 5000, MinSubtotalMinor: 10000,
		MaxPerCustomer: intp(1), Active: true}); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.Preview(ctx, "once", egp(t, 20000))
	if err != nil || d.Amount.Amount() != 5000 || d.Code != "ONCE" {
		t.Fatalf("preview = %+v, %v", d, err)
	}
	for code, want := range map[string]domain.Reason{"NOPE": domain.NotFound, "!!": domain.NotFound} {
		if _, err = f.svc.Preview(ctx, code, egp(t, 20000)); reason(err) != want {
			t.Errorf("%q: %v", code, err)
		}
	}
	if _, err = f.svc.Preview(ctx, "ONCE", egp(t, 9999)); reason(err) != domain.MinSubtotal {
		t.Fatalf("below minimum: %v", err)
	}
	mona := f.ids.New()
	if d, _, err = f.redeem(ctx, t, "ONCE", 20000, mona); err != nil || d.Amount.Amount() != 5000 {
		t.Fatalf("redeem = %+v, %v", d, err)
	}
	// The same customer again: refused; the quote cannot tell.
	if _, _, err = f.redeem(ctx, t, "ONCE", 20000, mona); reason(err) != domain.PerCustomer {
		t.Fatalf("again: %v", err)
	}
	if _, err = f.svc.Preview(ctx, "ONCE", egp(t, 20000)); err != nil {
		t.Fatalf("preview after: %v", err)
	}
	// Another store's code is no code here.
	other := f.store(t)
	if _, err = f.svc.Preview(other, "ONCE", egp(t, 20000)); reason(err) != domain.NotFound {
		t.Fatalf("other store: %v", err)
	}
	if _, _, err = f.redeem(other, t, "ONCE", 20000, mona); reason(err) != domain.NotFound {
		t.Fatalf("other store redeem: %v", err)
	}
}

// TestCouponPages (P6 review): the list comes a page at a time, newest
// first, every coupon once; a bad cursor or size is refused.
func TestCouponPages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	for i := range 7 {
		if _, err := f.svc.Create(ctx, domain.Coupon{Code: "PAGE" + strconv.Itoa(i), Kind: domain.FreeShipping, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	var codes []string
	cursor, pages := "", 0
	for {
		page, err := f.svc.Coupons(ctx, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, c := range page.Items {
			codes = append(codes, c.Code)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if pages != 3 || strings.Join(codes, ",") != "PAGE6,PAGE5,PAGE4,PAGE3,PAGE2,PAGE1,PAGE0" {
		t.Fatalf("%d pages: %v", pages, codes)
	}
	for name, call := range map[string]func() error{
		"cursor": func() error { _, err := f.svc.Coupons(ctx, "nope", 3); return err },
		"limit":  func() error { _, err := f.svc.Coupons(ctx, "", 101); return err },
	} {
		if err := call(); !errs.Is(err, errs.KindValidation) {
			t.Errorf("bad %s: %v", name, err)
		}
	}
}

// TestLastUseTakenOnce: twenty orders race for a coupon's last five uses.
func TestLastUseTakenOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	c, err := f.svc.Create(ctx, domain.Coupon{Code: "FIVE", Kind: domain.Percent, Percent: 20, MaxUses: intp(5), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var ok, used atomic.Int64
	var wg sync.WaitGroup
	errc := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, _, rErr := f.redeem(ctx, t, "FIVE", 10000, f.ids.New())
			switch {
			case rErr == nil:
				ok.Add(1)
			case reason(rErr) == domain.UsedUp:
				used.Add(1)
			default:
				errc <- rErr
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if ok.Load() != 5 || used.Load() != 15 {
		t.Fatalf("ok %d, used up %d", ok.Load(), used.Load())
	}
	if c, err = f.svc.Coupon(ctx, c.ID); err != nil || c.Uses != 5 {
		t.Fatalf("uses = %d, %v", c.Uses, err)
	}
}

// TestOncePerCustomerRace: one customer's concurrent orders take a
// once-per-customer coupon once, while other customers are not held up.
func TestOncePerCustomerRace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.Create(ctx, domain.Coupon{Code: "ONCE-EACH", Kind: domain.Percent, Percent: 10, MaxPerCustomer: intp(1), Active: true}); err != nil {
		t.Fatal(err)
	}
	mona, others := f.ids.New(), make([]ulid.ID, 5)
	for i := range others {
		others[i] = f.ids.New()
	}
	var monaOK, refused, othersOK atomic.Int64
	var wg sync.WaitGroup
	errc := make(chan error, 15)
	for i := range 15 {
		wg.Go(func() {
			customer := mona
			if i < len(others) {
				customer = others[i]
			}
			_, _, rErr := f.redeem(ctx, t, "ONCE-EACH", 10000, customer)
			switch {
			case rErr == nil && customer == mona:
				monaOK.Add(1)
			case rErr == nil:
				othersOK.Add(1)
			case reason(rErr) == domain.PerCustomer:
				refused.Add(1)
			default:
				errc <- rErr
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if monaOK.Load() != 1 || refused.Load() != 9 || othersOK.Load() != 5 {
		t.Fatalf("mona %d, refused %d, others %d", monaOK.Load(), refused.Load(), othersOK.Load())
	}
}

func TestReleaseOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	c, err := f.svc.Create(ctx, domain.Coupon{Code: "ONE", Kind: domain.FreeShipping, MaxUses: intp(1), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	d, order, err := f.redeem(ctx, t, "ONE", 1000, f.ids.New())
	if err != nil || !d.FreeShipping {
		t.Fatalf("redeem = %+v, %v", d, err)
	}
	if _, _, err = f.redeem(ctx, t, "ONE", 1000, f.ids.New()); reason(err) != domain.UsedUp {
		t.Fatalf("second: %v", err)
	}
	for range 2 { // a redelivered event gives back once
		if err = f.svc.ReleaseOrder(ctx, order); err != nil {
			t.Fatal(err)
		}
	}
	if c, err = f.svc.Coupon(ctx, c.ID); err != nil || c.Uses != 0 {
		t.Fatalf("uses after release = %d, %v", c.Uses, err)
	}
	if _, _, err = f.redeem(ctx, t, "ONE", 1000, f.ids.New()); err != nil {
		t.Fatalf("use after release: %v", err)
	}
	if err = f.svc.ReleaseOrder(ctx, f.ids.New()); err != nil {
		t.Fatalf("an order without a coupon: %v", err)
	}
}

// TestCodeLookupPlan: the checkout's lookup of a code is an index search
// on (store_id, code), for any store, under RLS.
func TestCodeLookupPlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	ctx := f.store(t)
	if _, err := f.svc.Create(ctx, domain.Coupon{Code: "PLAN", Kind: domain.FreeShipping, Active: true}); err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: pool, IDs: f.ids})
	if _, err := svc.Preview(ctx, "PLAN", egp(t, 1000)); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(ctx, t, pool, capture.Last(t, "FROM coupons"))
	testkit.AssertIndexCond(t, plan, "code = ")
	t.Log("\n" + plan)
}
