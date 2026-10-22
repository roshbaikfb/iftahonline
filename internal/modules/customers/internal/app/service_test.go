package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// recorder keeps audit entries and fails on any that carries a phone
// number: the audit log must never hold one.
type recorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if strings.Contains(fmt.Sprint(e), "+20") {
		return fmt.Errorf("audit entry carries a phone: %+v", e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recorder) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.Action + " " + e.TargetID
	}
	return out
}

type fixture struct {
	svc   *app.Service
	pool  *pgxpool.Pool
	qc    *testkit.QueryCounter
	audit *recorder
	ctxA  context.Context
	ctxB  context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, qc := testkit.CountingPool(t, infra.AppURL)
	ids := ulid.NewGenerator()
	rec := &recorder{}
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Audit: rec,
		Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	mk := func() context.Context {
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "cus-"+strings.ToLower(id.String()[14:]))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	return fixture{svc: svc, pool: pool, qc: qc, audit: rec, ctxA: mk(), ctxB: mk()}
}

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// order records an order the way the orders module does: in its own
// tenant transaction, which fails (and rolls back) with the call.
func (f fixture) order(ctx context.Context, p phone.E164, name string) (domain.Customer, error) {
	var c domain.Customer
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		c, txErr = f.svc.RecordOrderTx(ctx, tx, p, name)
		return txErr
	})
	return c, err
}

func TestRecordOrderTx(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := mustPhone(t, "01012345678")

	c1, err := f.order(f.ctxA, p, "  أحمد  علي ")
	if err != nil {
		t.Fatal(err)
	}
	if c1.Name != "أحمد علي" || c1.OrdersCount != 1 || c1.LastOrderAt == nil || c1.Phone != p || c1.Blocked() {
		t.Fatalf("first order: %+v", c1)
	}
	c2, err := f.order(f.ctxA, p, "")
	if err != nil || c2.ID != c1.ID || c2.Name != "أحمد علي" || c2.OrdersCount != 2 {
		t.Fatalf("second order without a name: %+v %v", c2, err)
	}
	c3, err := f.order(f.ctxA, p, "Ahmed")
	if err != nil || c3.Name != "Ahmed" || c3.OrdersCount != 3 {
		t.Fatalf("third order: %+v %v", c3, err)
	}
	// The same phone in store B is another customer.
	b, err := f.order(f.ctxB, p, "B")
	if err != nil || b.ID == c1.ID || b.OrdersCount != 1 {
		t.Fatalf("store B: %+v %v", b, err)
	}

	// Blocked: refused, and the refused order is not counted.
	if _, err = f.svc.Block(f.ctxA, c1.ID, "fake orders"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.order(f.ctxA, p, "x"); !errors.Is(err, app.ErrCustomerBlocked) {
		t.Fatalf("blocked order: %v", err)
	}
	got, err := f.svc.Get(f.ctxA, c1.ID)
	if err != nil || got.OrdersCount != 3 || got.Name != "Ahmed" {
		t.Fatalf("after refused order: %+v %v", got, err)
	}
	for ctx, want := range map[context.Context]bool{f.ctxA: true, f.ctxB: false} {
		if blocked, bErr := f.svc.Blocked(ctx, p); bErr != nil || blocked != want {
			t.Fatalf("Blocked = %v %v, want %v", blocked, bErr, want)
		}
	}
	if blocked, bErr := f.svc.Blocked(f.ctxA, mustPhone(t, "01111111111")); bErr != nil || blocked {
		t.Fatalf("unknown phone: %v %v", blocked, bErr)
	}

	_, err = f.order(f.ctxA, phone.E164{}, "x")
	wantKind(t, err, errs.KindValidation)
	_, err = f.order(f.ctxA, mustPhone(t, "01222222222"), strings.Repeat("x", domain.MaxNameLen+1))
	wantKind(t, err, errs.KindValidation)
}

func TestListAndLookup(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ahmed, _ := f.order(f.ctxA, mustPhone(t, "01000000001"), "أحمد علي")
	sara, _ := f.order(f.ctxA, mustPhone(t, "01000000002"), "Sara 50%_off")
	mona, _ := f.order(f.ctxA, mustPhone(t, "01000000003"), "منى")
	if _, err := f.order(f.ctxB, mustPhone(t, "01000000004"), "أحمد من متجر آخر"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Block(f.ctxA, sara.ID, ""); err != nil {
		t.Fatal(err)
	}
	ids := func(p app.Page) []ulid.ID {
		out := make([]ulid.ID, len(p.Items))
		for i, c := range p.Items {
			out[i] = c.ID
		}
		return out
	}
	tests := []struct {
		name string
		q    domain.Query
		want []ulid.ID
	}{
		{"all, newest first", domain.Query{}, []ulid.ID{mona.ID, sara.ID, ahmed.ID}},
		{"phone in national form", domain.Query{Text: "0100 000 0002"}, []ulid.ID{sara.ID}},
		{"phone international", domain.Query{Text: "+201000000001"}, []ulid.ID{ahmed.ID}},
		{"name without hamza", domain.Query{Text: "احمد"}, []ulid.ID{ahmed.ID}},
		{"second word", domain.Query{Text: "عل"}, []ulid.ID{ahmed.ID}},
		{"wildcards are literal", domain.Query{Text: "50%_"}, []ulid.ID{sara.ID}},
		{"a % alone matches nothing", domain.Query{Text: "%%"}, nil},
		{"block list", domain.Query{Blocked: true}, []ulid.ID{sara.ID}},
		{"another store's phone", domain.Query{Text: "01000000004"}, nil},
	}
	for _, tt := range tests {
		p, err := f.svc.List(f.ctxA, tt.q)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := ids(p); fmt.Sprint(got) != fmt.Sprint(tt.want) && (len(got) != 0 || len(tt.want) != 0) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}

	// Pages: 2 + 1, the cursor is the last ID of the page.
	p1, err := f.svc.List(f.ctxA, domain.Query{Limit: 2})
	if err != nil || len(p1.Items) != 2 || p1.Next == nil || *p1.Next != sara.ID {
		t.Fatalf("page 1: %v %v", ids(p1), err)
	}
	p2, err := f.svc.List(f.ctxA, domain.Query{Limit: 2, After: p1.Next})
	if err != nil || fmt.Sprint(ids(p2)) != fmt.Sprint([]ulid.ID{ahmed.ID}) || p2.Next != nil {
		t.Fatalf("page 2: %v %v", ids(p2), err)
	}

	// One query per page whatever its size (plus BEGIN, tenant, COMMIT).
	f.qc.Reset()
	if _, err = f.svc.List(f.ctxA, domain.Query{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	one := f.qc.Count()
	f.qc.Reset()
	if _, err = f.svc.List(f.ctxA, domain.Query{Limit: 100}); err != nil {
		t.Fatal(err)
	}
	if many := f.qc.Count(); many != one || one > 4 {
		t.Fatalf("queries: %d for 1, %d for 100", one, many)
	}
	_, err = f.svc.List(f.ctxA, domain.Query{Text: "ا"})
	wantKind(t, err, errs.KindValidation)
}

func TestBlockList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// A phone that never ordered.
	c, err := f.svc.BlockPhone(f.ctxA, "010 9999 9999", "  spam ")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Blocked() || c.BlockReason != "spam" || c.OrdersCount != 0 || c.Phone != mustPhone(t, "01099999999") {
		t.Fatalf("blocked phone: %+v", c)
	}
	first := *c.BlockedAt
	again, err := f.svc.BlockPhone(f.ctxA, "+201099999999", "still spam")
	if err != nil || again.ID != c.ID || !again.BlockedAt.Equal(first) || again.BlockReason != "still spam" {
		t.Fatalf("block again: %+v %v", again, err)
	}
	if _, err = f.order(f.ctxA, c.Phone, "x"); !errors.Is(err, app.ErrCustomerBlocked) {
		t.Fatalf("order from a blocked phone: %v", err)
	}

	un, err := f.svc.Unblock(f.ctxA, c.ID)
	if err != nil || un.Blocked() || un.BlockReason != "" {
		t.Fatalf("unblock: %+v %v", un, err)
	}
	if un, err = f.svc.Unblock(f.ctxA, c.ID); err != nil || un.Blocked() {
		t.Fatalf("unblock twice: %+v %v", un, err)
	}
	if _, err = f.order(f.ctxA, c.Phone, "x"); err != nil {
		t.Fatalf("order after unblock: %v", err)
	}
	if b, bErr := f.svc.Block(f.ctxA, c.ID, "again"); bErr != nil || !b.Blocked() {
		t.Fatalf("block by id: %+v %v", b, bErr)
	}

	// Store B cannot reach A's customer.
	if _, err = f.svc.Block(f.ctxB, c.ID, "x"); !errors.Is(err, app.ErrCustomerNotFound) {
		t.Fatalf("B blocks A's customer: %v", err)
	}
	if _, err = f.svc.Unblock(f.ctxB, c.ID); !errors.Is(err, app.ErrCustomerNotFound) {
		t.Fatalf("B unblocks A's customer: %v", err)
	}
	if _, err = f.svc.Get(f.ctxB, c.ID); !errors.Is(err, app.ErrCustomerNotFound) {
		t.Fatalf("B reads A's customer: %v", err)
	}
	if got, _ := f.svc.Get(f.ctxA, c.ID); !got.Blocked() || got.BlockReason != "again" {
		t.Fatalf("A's customer after B's attempts: %+v", got)
	}

	want := []string{"customer.blocked " + c.ID.String(), "customer.blocked " + c.ID.String(),
		"customer.unblocked " + c.ID.String(), "customer.unblocked " + c.ID.String(), "customer.blocked " + c.ID.String()}
	if fmt.Sprint(f.audit.actions()) != fmt.Sprint(want) {
		t.Fatalf("audit = %v", f.audit.actions())
	}

	_, err = f.svc.BlockPhone(f.ctxA, "12", "")
	wantKind(t, err, errs.KindValidation)
	_, err = f.svc.BlockPhone(f.ctxA, "01012345678", strings.Repeat("x", domain.MaxReasonLen+1))
	wantKind(t, err, errs.KindValidation)
	_, err = f.svc.Block(f.ctxA, c.ID, strings.Repeat("x", domain.MaxReasonLen+1))
	wantKind(t, err, errs.KindValidation)
}

func TestRegionFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	boom := errors.New("tenancy down")
	svc := app.NewService(app.Deps{DB: f.pool, IDs: ulid.NewGenerator(),
		Region: func(context.Context) (phone.Region, error) { return "", boom }})
	if _, err := svc.List(f.ctxA, domain.Query{}); !errors.Is(err, boom) {
		t.Fatalf("List: %v", err)
	}
	if _, err := svc.BlockPhone(f.ctxA, "01012345678", ""); !errors.Is(err, boom) {
		t.Fatalf("BlockPhone: %v", err)
	}
}

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if !errs.Is(err, k) {
		t.Fatalf("err = %v, want kind %v", err, k)
	}
}

// TestPhoneLookupUsesIndexUnderRLS: Blocked runs before every order, so
// the phone lookup must be an index search as app_user under a generic
// plan (text = is leakproof; ADR-0013's citext lesson), and the list
// walks the store's index instead of sorting the store.
func TestPhoneLookupUsesIndexUnderRLS(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	svc := app.NewService(app.Deps{DB: pool, IDs: ulid.NewGenerator(),
		Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	if _, err := svc.Blocked(f.ctxA, mustPhone(t, "01012345678")); err != nil {
		t.Fatal(err)
	}
	testkit.AssertIndexCond(t, testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, "phone = $")), "phone")

	// A large store, analyzed, so the planner sees realistic statistics.
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	store, err := tenant.FromContext(f.ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.InTenantTx(f.ctxA, owner, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(f.ctxA, `INSERT INTO customers (id, store_id, phone, name, name_key)
			SELECT gen_random_uuid(), $1, '+2010' || lpad(n::text, 8, '0'), 'n' || n, 'n' || n FROM generate_series(1, 20000) AS n`, store.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(t.Context(), "ANALYZE customers"); err != nil {
		t.Fatal(err)
	}
	page, err := svc.List(f.ctxA, domain.Query{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.List(f.ctxA, domain.Query{Limit: 25, After: page.Next}); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, "ORDER BY id DESC"))
	testkit.AssertIndexCond(t, plan, "id <")
	if strings.Contains(plan, "Sort") {
		t.Fatalf("the list sorts instead of walking the index:\n%s", plan)
	}
}
