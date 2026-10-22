package app_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func ids(p app.Page) []ulid.ID {
	out := make([]ulid.ID, len(p.Items))
	for i, o := range p.Items {
		out[i] = o.ID
	}
	return out
}

func TestListFilters(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "منتج", 1000, 100)
	place := func(ph, gov string) domain.Order {
		t.Helper()
		in := merchantOrder(domain.LineInput{VariantID: v, Qty: 1})
		in.Phone, in.Governorate = ph, gov
		p, err := f.svc.PlaceOrder(f.ctxA, in)
		if err != nil {
			t.Fatal(err)
		}
		return p.Order
	}
	o1 := place("01011111111", "EG-C")
	o2 := place("01022222222", "EG-GZ")
	o3 := place("01011111111", "EG-GZ")
	o4 := place("01033333333", "EG-C")
	if _, err := f.svc.Transition(f.ctxA, o2.ID, domain.StatusConfirmed, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.PlaceOrder(f.ctxB, merchantOrder(domain.LineInput{VariantID: f.product(t, f.storeB, "B", 1000, 5), Qty: 1})); err != nil {
		t.Fatal(err)
	}
	pending, confirmed := domain.StatusPending, domain.StatusConfirmed
	day := time.Now().UTC().Add(-time.Hour)
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	tests := []struct {
		name string
		q    domain.Query
		want []ulid.ID
	}{
		{"all, newest first", domain.Query{}, []ulid.ID{o4.ID, o3.ID, o2.ID, o1.ID}},
		{"pending", domain.Query{Status: &pending}, []ulid.ID{o4.ID, o3.ID, o1.ID}},
		{"confirmed", domain.Query{Status: &confirmed}, []ulid.ID{o2.ID}},
		{"governorate", domain.Query{Governorate: "eg-gz"}, []ulid.ID{o3.ID, o2.ID}},
		{"status and governorate", domain.Query{Status: &pending, Governorate: "EG-GZ"}, []ulid.ID{o3.ID}},
		{"phone, national form", domain.Query{Phone: "010 1111 1111"}, []ulid.ID{o3.ID, o1.ID}},
		{"phone and governorate", domain.Query{Phone: "+201011111111", Governorate: "EG-C"}, []ulid.ID{o1.ID}},
		{"customer history", domain.Query{CustomerID: &o1.CustomerID}, []ulid.ID{o3.ID, o1.ID}},
		{"customer and status", domain.Query{CustomerID: &o2.CustomerID, Status: &pending}, nil},
		{"date range", domain.Query{From: &day, To: &tomorrow}, []ulid.ID{o4.ID, o3.ID, o2.ID, o1.ID}},
		{"future range", domain.Query{From: &tomorrow}, nil},
	}
	for _, tt := range tests {
		p, err := f.svc.List(f.ctxA, tt.q)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if fmt.Sprint(ids(p)) != fmt.Sprint(tt.want) && (len(p.Items) != 0 || len(tt.want) != 0) {
			t.Errorf("%s: got %v, want %v", tt.name, ids(p), tt.want)
		}
	}

	// Pages of 3 + 1, and one query per page whatever its size.
	p1, err := f.svc.List(f.ctxA, domain.Query{Limit: 3})
	if err != nil || len(p1.Items) != 3 || p1.Next == nil || *p1.Next != o2.ID {
		t.Fatalf("page 1: %v %v", ids(p1), err)
	}
	p2, err := f.svc.List(f.ctxA, domain.Query{Limit: 3, Before: p1.Next})
	if err != nil || fmt.Sprint(ids(p2)) != fmt.Sprint([]ulid.ID{o1.ID}) || p2.Next != nil {
		t.Fatalf("page 2: %v %v", ids(p2), err)
	}
	f.qc.Reset()
	if _, err = f.svc.List(f.ctxA, domain.Query{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	one := f.qc.Count()
	f.qc.Reset()
	if _, err = f.svc.List(f.ctxA, domain.Query{Limit: 100}); err != nil {
		t.Fatal(err)
	}
	if many := f.qc.Count(); one != many || one > 4 {
		t.Fatalf("queries: %d for 1, %d for 100", one, many)
	}
	// The detail is a constant number of queries too.
	f.qc.Reset()
	if _, err = f.svc.GetDetail(f.ctxA, o1.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.qc.Count(); n > 6 {
		t.Fatalf("detail queries: %d", n)
	}
	if _, err = f.svc.List(f.ctxA, domain.Query{Phone: "123"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad phone filter: %v", err)
	}
}

func TestExport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "=HYPERLINK(\"x\")", 12345, 100)
	in := merchantOrder(domain.LineInput{VariantID: v, Qty: 2})
	in.Name, in.Notes = "@attacker", "-1+1"
	p, err := f.svc.PlaceOrder(f.ctxA, in)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 { // more orders than one page of the test
		if _, err = f.svc.PlaceOrder(f.ctxA, merchantOrder(domain.LineInput{VariantID: v, Qty: 1})); err != nil {
			t.Fatal(err)
		}
	}
	f.audit.take()
	var buf bytes.Buffer
	n, err := f.svc.Export(f.ctxA, domain.Query{}, &buf)
	if err != nil || n != 4 {
		t.Fatalf("export: %d rows, %v", n, err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("\xef\xbb\xbf")) {
		t.Fatal("no UTF-8 BOM")
	}
	recs, err := csv.NewReader(bytes.NewReader(buf.Bytes()[3:])).ReadAll()
	if err != nil || len(recs) != 5 {
		t.Fatalf("csv: %d records, %v", len(recs), err)
	}
	last := recs[4] // oldest: the attacker's order
	col := map[string]string{}
	for i, h := range recs[0] {
		col[h] = last[i]
	}
	cairo, _ := time.LoadLocation("Africa/Cairo")
	want := map[string]string{
		"order": p.Order.Code(), "status": "pending", "name": "'@attacker", "notes": "'-1+1", "phone": "'+201012345678",
		"governorate": "الإسكندرية", "items": "2× =HYPERLINK(\"x\")", "item_count": "2",
		"subtotal": "246.90", "shipping": "0.00", "total": "246.90", "currency": "EGP",
		"created_at": p.Order.CreatedAt.In(cairo).Format("2006-01-02 15:04"),
	}
	for k, w := range want {
		if col[k] != w {
			t.Errorf("%s = %q, want %q", k, col[k], w)
		}
	}
	if got := f.audit.take(); len(got) != 1 || !strings.HasPrefix(got[0], "order.exported") {
		t.Fatalf("audit %v", got)
	}
	// Store B's export is empty (header only).
	buf.Reset()
	if n, err = f.svc.Export(f.ctxB, domain.Query{}, &buf); err != nil || n != 0 {
		t.Fatalf("store B export: %d %v", n, err)
	}
	if _, err = f.svc.Export(f.ctxA, domain.Query{Limit: -1}, &buf); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad query: %v", err)
	}
}

// TestOrderQueriesUseIndexesUnderRLS seeds one large store across two
// months and checks, as app_user under generic plans, that each list
// walks its driving index without sorting, pages by an index bound, and
// that a lookup by ID touches one partition.
func TestOrderQueriesUseIndexesUnderRLS(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	store, _ := tenant.FromContext(f.ctxA)
	// 40,000 orders over this month and the next, 20 per customer.
	if err = postgres.InTenantTx(f.ctxA, owner, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(f.ctxA, `
INSERT INTO orders (id, store_id, created_at, status, source, customer_id, name, phone, governorate, city, address,
                    currency, subtotal_minor, total_minor, item_count, lines_hash)
SELECT gen_random_uuid(), $1, date_trunc('month', now()) + (n % 50) * interval '1 day' + n * interval '1 second',
       (ARRAY['pending','confirmed','shipped','delivered'])[1 + n % 4], 'storefront',
       md5((n / 20)::text)::uuid, 'nn', '+2010' || lpad((n / 20)::text, 8, '0'), 'EG-C', 'city', 'address 1',
       'EGP', 1000, 1000, 1, decode(md5(n::text) || md5(n::text), 'hex')
FROM generate_series(1, 40000) AS n`, store.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(t.Context(), "ANALYZE orders"); err != nil {
		t.Fatal(err)
	}
	v := f.product(t, f.storeA, "x", 1000, 10)
	placed, err := f.svc.PlaceOrder(f.ctxA, merchantOrder(domain.LineInput{VariantID: v, Qty: 1}))
	if err != nil {
		t.Fatal(err)
	}

	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	cairo, _ := time.LoadLocation("Africa/Cairo")
	svc := app.NewService(app.Deps{DB: pool, IDs: ulid.NewGenerator(), Catalog: f.catalog, Customers: f.cust, Auth: policy(t),
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: money.EGP, Country: geo.EG, Location: cairo}, nil
		}})
	pending := domain.StatusPending
	cust := placed.Order.CustomerID
	for _, tt := range []struct {
		name, marker, index string
		q                   domain.Query
	}{
		{"all", "ListOrders", "store_id", domain.Query{}},
		{"status", "status = $2", "status", domain.Query{Status: &pending}},
		{"phone", "phone = $2", "phone", domain.Query{Phone: "01000000007"}},
		{"customer", "customer_id = $2", "customer_id", domain.Query{CustomerID: &cust}},
	} {
		page, err := svc.List(f.ctxA, tt.q)
		if err != nil {
			t.Fatal(err)
		}
		if page.Next != nil { // the second page: the keyset bound in use
			q := tt.q
			q.Before = page.Next
			if _, err = svc.List(f.ctxA, q); err != nil {
				t.Fatal(err)
			}
		}
		sql := lastMatching(t, capture, tt.marker)
		plan := testkit.GenericPlan(f.ctxA, t, f.pool, sql)
		testkit.AssertIndexCond(t, plan, tt.index)
		testkit.AssertIndexCond(t, plan, "created_at")
		if strings.Contains(plan, "Sort") || strings.Contains(plan, "Seq Scan") {
			t.Errorf("%s: list sorts or scans:\n%s", tt.name, plan)
		}
	}
	// The duplicate-window check runs before every storefront order.
	if _, err = svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: 1})); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, "lines_hash = $"))
	testkit.AssertIndexCond(t, plan, "phone")
	testkit.AssertIndexCond(t, plan, "created_at")

	if _, err = svc.Get(f.ctxA, placed.Order.ID); err != nil {
		t.Fatal(err)
	}
	plan = testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, "AND created_at = $2 AND id = $3"))
	if strings.Count(plan, "Index Scan") != 1 {
		t.Fatalf("lookup by ID is not one partition:\n%s", plan)
	}
}

// lastMatching finds the list query by a marker: "ListOrders" matches the
// unfiltered list (the only one without a driving filter).
func lastMatching(t *testing.T, c *testkit.SQLCapture, marker string) pgx.TraceQueryStartData {
	t.Helper()
	if marker == "ListOrders" {
		return c.Last(t, "AND created_at >= $2 AND created_at < $3")
	}
	return c.Last(t, marker)
}
