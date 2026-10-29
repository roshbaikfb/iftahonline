package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/ordertest"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	infra       testinfra.Infra
	crossTenant testkit.Registry
)

// TestMain fails the package if any orders operation under
// /v1/stores/{store_id} lacks a cross-tenant test (ADR-0006).
func TestMain(m *testing.M) {
	code := testinfra.Run(m, &infra)
	if code == 0 && !testing.Short() && runAll() {
		_, file, _, _ := runtime.Caller(0)
		spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "api", "openapi.yaml"))
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "load spec:", err)
			os.Exit(1)
		}
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "orders")); len(missing) > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "operations without a cross-tenant test:", missing)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

func runAll() bool {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.run=") || strings.HasPrefix(a, "-test.fuzz=") {
			return false
		}
	}
	return true
}

type fixture struct {
	env            *ordertest.Env
	h              http.Handler
	storeA, storeB tenant.StoreID
	ctxB           context.Context
	variantA       ulid.ID
	orderB         domain.Order // store B's, with secrets in it
	tok            map[authz.Role]string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := ordertest.New(t, infra.AppURL)
	_, a := e.NewStore(t)
	ctxB, b := e.NewStore(t)
	f := fixture{env: e, storeA: a, storeB: b, ctxB: ordertest.As(ctxB, t, authz.RoleOwner), tok: map[authz.Role]string{}}
	for _, r := range []authz.Role{authz.RoleOwner, authz.RoleSupport, authz.RoleShipping} {
		tok := string(r) + "-a"
		e.Members[tok] = testkit.FakeMember{UserID: e.IDs.New(), Roles: map[tenant.StoreID]authz.Role{a: r}}
		f.tok[r] = tok
	}
	f.variantA = e.Product(t, a, "تيشيرت", 15000, 20)
	vb := e.Product(t, b, "B secret product", 99900, 5)
	p, err := e.Svc.PlaceOrder(f.ctxB, domain.NewOrder{Source: domain.SourceMerchant, Name: "B secret name", Phone: "01055555555",
		Governorate: "EG-C", City: "B city", Address: "B secret address 1", Lines: []domain.LineInput{{VariantID: vb, Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	f.orderB = p.Order
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	httpapi.Mount(mux, e.Svc, e.Policy, testkit.FakeStoreScope(resp, e.Members), resp)
	f.h = mux
	return f
}

func path(s tenant.StoreID, rest string) string { return "/v1/stores/" + s.String() + "/orders" + rest }

func req(t *testing.T, method, p, token, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rq := httptest.NewRequestWithContext(t.Context(), method, p, r)
	if token != "" {
		rq.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		rq.Header.Set("Content-Type", "application/json")
	}
	return rq
}

func (f fixture) do(t *testing.T, rq *http.Request) (int, map[string]any, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	var m map[string]any
	if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, m, rec.Header()
}

func (f fixture) orderBody() string {
	return fmt.Sprintf(`{"name":"منى أحمد","phone":"010 1234 5678","governorate":"EG-ALX","city":"سموحة","address":"14 شارع فوزي معاذ",
		"lines":[{"variant_id":%q,"qty":2}]}`, f.variantA)
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, owner := f.storeA, f.tok[authz.RoleOwner]

	rq := req(t, http.MethodPost, path(a, ""), owner, f.orderBody())
	rq.Header.Set("Idempotency-Key", "phone-order-1")
	code, d, _ := f.do(t, rq)
	o, _ := d["order"].(map[string]any)
	if code != http.StatusCreated || o["status"] != "pending" || o["total_minor"] != float64(30000) || o["source"] != "merchant" ||
		o["phone"] != "+201012345678" || len(d["lines"].([]any)) != 1 || len(d["events"].([]any)) != 1 {
		t.Fatalf("createOrder = %d %v", code, d)
	}
	id := o["id"].(string)
	if !strings.HasSuffix(id, o["code"].(string)) {
		t.Fatalf("code %v of %s", o["code"], id)
	}
	// The retry replays.
	rq = req(t, http.MethodPost, path(a, ""), owner, f.orderBody())
	rq.Header.Set("Idempotency-Key", "phone-order-1")
	code, d, hdr := f.do(t, rq)
	if code != http.StatusOK || hdr.Get("Idempotent-Replayed") != "true" || d["order"].(map[string]any)["id"] != id {
		t.Fatalf("replay = %d %v", code, d)
	}

	if code, d, _ = f.do(t, req(t, http.MethodGet, path(a, "/"+id), f.tok[authz.RoleShipping], "")); code != http.StatusOK || d["order"].(map[string]any)["id"] != id {
		t.Fatalf("getOrder = %d %v", code, d)
	}
	code, page, _ := f.do(t, req(t, http.MethodGet, path(a, "?status=pending&governorate=EG-ALX&phone=01012345678"), owner, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 {
		t.Fatalf("listOrders = %d %v", code, page)
	}
	code, moved, _ := f.do(t, req(t, http.MethodPost, path(a, "/"+id+"/transitions"), f.tok[authz.RoleSupport], `{"status":"confirmed","version":1,"note":"أكدت هاتفيًا"}`))
	if code != http.StatusOK || moved["status"] != "confirmed" || moved["version"] != float64(2) {
		t.Fatalf("transitionOrder = %d %v", code, moved)
	}

	// Bulk: the confirmed one fails, a new pending one moves.
	rq = req(t, http.MethodPost, path(a, ""), owner, strings.Replace(f.orderBody(), `"qty":2`, `"qty":1`, 1))
	code, d2, _ := f.do(t, rq)
	if code != http.StatusCreated {
		t.Fatal(code, d2)
	}
	id2 := d2["order"].(map[string]any)["id"].(string)
	code, bulk, _ := f.do(t, req(t, http.MethodPost, path(a, "/transitions"), owner, fmt.Sprintf(`{"order_ids":[%q,%q],"status":"confirmed"}`, id2, id)))
	res, _ := bulk["results"].([]any)
	if code != http.StatusOK || len(res) != 2 {
		t.Fatalf("bulk = %d %v", code, bulk)
	}
	r0, r1 := res[0].(map[string]any), res[1].(map[string]any)
	if r0["ok"] != true || r0["order"].(map[string]any)["status"] != "confirmed" ||
		r1["ok"] != false || r1["error"].(map[string]any)["code"] != "invalid_transition" {
		t.Fatalf("bulk results %v", res)
	}

	// Export: a CSV attachment with both orders.
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req(t, http.MethodGet, path(a, "/export?status=confirmed"), owner, ""))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/csv" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment; filename=\"orders-") {
		t.Fatalf("export = %d %v", rec.Code, rec.Header())
	}
	if lines := bytes.Count(rec.Body.Bytes(), []byte("\n")); lines != 3 || !bytes.Contains(rec.Body.Bytes(), []byte("منى أحمد")) {
		t.Fatalf("export body (%d lines): %s", lines, rec.Body)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, owner := f.storeA, f.tok[authz.RoleOwner]
	tests := []struct {
		name, method, p, body string
		want                  int
		code                  string
	}{
		{"bad phone", http.MethodPost, "", strings.Replace(f.orderBody(), "010 1234 5678", "123", 1), 422, "validation_failed"},
		{"bad governorate", http.MethodPost, "", strings.Replace(f.orderBody(), "EG-ALX", "SA-01", 1), 422, "validation_failed"},
		{"bad variant id", http.MethodPost, "", `{"name":"nn","phone":"01012345678","governorate":"EG-C","city":"cc","address":"aaaaa","lines":[{"variant_id":"x","qty":1}]}`, 422, "validation_failed"},
		{"unknown variant", http.MethodPost, "", strings.Replace(f.orderBody(), f.variantA.String(), ulid.NewGenerator().New().String(), 1), 422, "validation_failed"},
		{"too much", http.MethodPost, "", strings.Replace(f.orderBody(), `"qty":2`, `"qty":50`, 1), 409, "insufficient_stock"},
		{"no body", http.MethodPost, "", "", 400, ""},
		{"bad json", http.MethodPost, "", "{", 400, ""},
		{"bad status filter", http.MethodGet, "?status=paid", "", 422, "validation_failed"},
		{"bad cursor", http.MethodGet, "?cursor=x", "", 422, "validation_failed"},
		{"bad customer", http.MethodGet, "?customer_id=x", "", 422, "validation_failed"},
		{"bad limit", http.MethodGet, "?limit=500", "", 422, "validation_failed"},
		{"bad dates", http.MethodGet, "?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", "", 422, "validation_failed"},
		{"malformed id", http.MethodGet, "/nope", "", 404, "order_not_found"},
		{"forbidden move", http.MethodPost, "/" + f.orderB.ID.String() + "/transitions", `{"status":"confirmed"}`, 404, "order_not_found"},
		{"bad target", http.MethodPost, "/" + ulid.NewGenerator().New().String() + "/transitions", `{"status":"paid"}`, 422, "validation_failed"},
		{"bulk too big", http.MethodPost, "/transitions", `{"order_ids":[],"status":"confirmed"}`, 422, "validation_failed"},
		{"bulk bad id", http.MethodPost, "/transitions", `{"order_ids":["x"],"status":"confirmed"}`, 422, "validation_failed"},
		{"export bad dates", http.MethodGet, "/export?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", "", 422, "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := f.do(t, req(t, tt.method, path(a, tt.p), owner, tt.body))
			if code != tt.want || (tt.code != "" && body["code"] != tt.code) {
				t.Fatalf("%s %s = %d %v, want %d %s", tt.method, tt.p, code, body, tt.want, tt.code)
			}
		})
	}
	// A reused key with another order.
	for i, body := range []string{f.orderBody(), strings.Replace(f.orderBody(), `"qty":2`, `"qty":1`, 1)} {
		rq := req(t, http.MethodPost, path(a, ""), owner, body)
		rq.Header.Set("Idempotency-Key", "reused")
		code, d, _ := f.do(t, rq)
		if want := []int{201, 409}[i]; code != want || (i == 1 && d["code"] != "idempotency_key_reused") {
			t.Fatalf("key use %d = %d %v", i, code, d)
		}
	}
}

// TestAuthorization: support confirms but cannot ship or export;
// shipping ships but cannot confirm, create or export; nobody without a
// token gets in.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	p, err := f.env.Svc.PlaceOrder(ordertest.As(tenant.WithStore(t.Context(), a), t, authz.RoleOwner), domain.NewOrder{Source: domain.SourceMerchant,
		Name: "nn", Phone: "01012345678", Governorate: "EG-C", City: "cc", Address: "aaaaa", Lines: []domain.LineInput{{VariantID: f.variantA, Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	id := p.Order.ID.String()
	support, shipping := f.tok[authz.RoleSupport], f.tok[authz.RoleShipping]
	tests := []struct {
		token, method, p, body string
		want                   int
	}{
		{shipping, http.MethodPost, "/" + id + "/transitions", `{"status":"confirmed"}`, 403},
		{shipping, http.MethodPost, "", f.orderBody(), 403},
		{shipping, http.MethodGet, "/export", "", 403},
		{support, http.MethodGet, "/export", "", 403},
		{support, http.MethodPost, "/" + id + "/transitions", `{"status":"confirmed"}`, 200},
		{support, http.MethodPost, "/" + id + "/transitions", `{"status":"ready_to_ship"}`, 403},
		{shipping, http.MethodPost, "/" + id + "/transitions", `{"status":"ready_to_ship"}`, 200},
		{"", http.MethodGet, "", "", 401},
		{"", http.MethodGet, "/counts", "", 401},
		{shipping, http.MethodGet, "/counts", "", 200},
	}
	for _, tt := range tests {
		if code, body, _ := f.do(t, req(t, tt.method, path(a, tt.p), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.p, code, body, tt.want)
		}
	}
	// A bulk by someone without the move's permission fails per order.
	code, bulk, _ := f.do(t, req(t, http.MethodPost, path(a, "/transitions"), support, fmt.Sprintf(`{"order_ids":[%q],"status":"shipped"}`, id)))
	if r := bulk["results"].([]any)[0].(map[string]any); code != 200 || r["ok"] != false || r["error"].(map[string]any)["code"] != "forbidden" {
		t.Fatalf("bulk by support = %d %v", code, bulk)
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b, owner := f.storeA, f.storeB, f.tok[authz.RoleOwner]
	oid := f.orderB.ID.String()
	leaks := []string{oid, f.orderB.Code(), "B secret name", "B secret address", "B secret product", "+201055555555", f.orderB.CustomerID.String()}
	unchanged := func(t testing.TB) {
		t.Helper()
		got, err := f.env.Svc.Get(f.ctxB, f.orderB.ID)
		if err != nil || got.Status != domain.StatusPending || got.Version != 1 {
			t.Fatalf("store B's order changed: %+v %v", got, err)
		}
		page, err := f.env.Svc.List(f.ctxB, domain.Query{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("store B's orders: %d %v", len(page.Items), err)
		}
	}
	move := `{"status":"confirmed"}`
	bulk := fmt.Sprintf(`{"order_ids":[%q],"status":"confirmed"}`, oid)
	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "listOrders", Request: req(t, http.MethodGet, path(b, ""), owner, ""), Leaks: leaks},
		{OperationID: "createOrder", Request: req(t, http.MethodPost, path(b, ""), owner, f.orderBody()), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getOrder", Request: req(t, http.MethodGet, path(b, "/"+oid), owner, ""), Leaks: leaks},
		{OperationID: "transitionOrder", Request: req(t, http.MethodPost, path(b, "/"+oid+"/transitions"), owner, move), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "bulkTransitionOrders", Request: req(t, http.MethodPost, path(b, "/transitions"), owner, bulk), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "exportOrders", Request: req(t, http.MethodGet, path(b, "/export"), owner, ""), Leaks: leaks},
		{OperationID: "countOrders", Request: req(t, http.MethodGet, path(b, "/counts"), owner, ""), Leaks: leaks},
		{OperationID: "getOrderStats", Request: req(t, http.MethodGet, path(b, "/stats?range=30d"), owner, ""), Leaks: leaks},
		// A's store in the path, B's order.
		{OperationID: "getOrder", Request: req(t, http.MethodGet, path(a, "/"+oid), owner, ""), Leaks: leaks},
		{OperationID: "transitionOrder", Request: req(t, http.MethodPost, path(a, "/"+oid+"/transitions"), owner, move), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// Through A's own store, B's order is never listed, exported or moved
	// in bulk (the bulk reports it as not found).
	for _, p := range []string{"", "?phone=01055555555", "?customer_id=" + f.orderB.CustomerID.String()} {
		if code, page, _ := f.do(t, req(t, http.MethodGet, path(a, p), owner, "")); code != 200 || len(page["items"].([]any)) != 0 {
			t.Fatalf("A lists %q = %d %v", p, code, page)
		}
	}
	code, res, _ := f.do(t, req(t, http.MethodPost, path(a, "/transitions"), owner, bulk))
	if r := res["results"].([]any)[0].(map[string]any); code != 200 || r["ok"] != false || r["error"].(map[string]any)["code"] != "order_not_found" {
		t.Fatalf("A bulk-moves B's order = %d %v", code, res)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req(t, http.MethodGet, path(a, "/export"), owner, ""))
	for _, l := range leaks {
		if strings.Contains(rec.Body.String(), l) {
			t.Fatalf("A's export leaks %q", l)
		}
	}
	unchanged(t)
}

// TestCountOrders: every status with its count, zeros included, for the
// store in the path only.
func TestCountOrders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, owner := f.storeA, f.tok[authz.RoleOwner]
	ctx := ordertest.As(tenant.WithStore(t.Context(), a), t, authz.RoleOwner)
	for i := range 3 {
		p, err := f.env.Svc.PlaceOrder(ctx, domain.NewOrder{Source: domain.SourceMerchant, Name: "nn", Phone: fmt.Sprintf("0101234567%d", i),
			Governorate: "EG-C", City: "cc", Address: "aaaaa", Lines: []domain.LineInput{{VariantID: f.variantA, Qty: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err = f.env.Svc.Transition(ctx, p.Order.ID, domain.StatusConfirmed, "", 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	code, counts, _ := f.do(t, req(t, http.MethodGet, path(a, "/counts"), owner, ""))
	if code != http.StatusOK || counts["pending"] != float64(2) || counts["confirmed"] != float64(1) || counts["delivered"] != float64(0) || len(counts) != 8 {
		t.Fatalf("countOrders = %d %v (store B's pending order must not count)", code, counts)
	}
}

// The dashboard home's sums: only the caller's store, and only a known
// range.
func TestOrderStats(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, owner := f.storeA, f.tok[authz.RoleOwner]
	ctx := ordertest.As(tenant.WithStore(t.Context(), a), t, authz.RoleOwner)
	p, err := f.env.Svc.PlaceOrder(ctx, domain.NewOrder{Source: domain.SourceMerchant, Name: "nn", Phone: "01012345670",
		Governorate: "EG-C", City: "cc", Address: "aaaaa", Lines: []domain.LineInput{{VariantID: f.variantA, Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	code, st, _ := f.do(t, req(t, http.MethodGet, path(a, "/stats"), owner, ""))
	sales, _ := st["sales"].(map[string]any)
	daily, _ := st["daily"].([]any)
	if code != http.StatusOK || st["range"] != "today" || st["currency"] != "EGP" || sales["orders"] != float64(1) ||
		sales["total_minor"] != float64(p.Order.Total.Amount()) || len(daily) != 30 || st["oldest_pending_at"] == nil {
		t.Fatalf("getOrderStats = %d %v (store B's order must not count)", code, st)
	}
	if code, body, _ := f.do(t, req(t, http.MethodGet, path(a, "/stats?range=1y"), owner, "")); code != http.StatusUnprocessableEntity {
		t.Fatalf("range 1y = %d %v", code, body)
	}
	if code, body, _ := f.do(t, req(t, http.MethodGet, path(a, "/stats"), "", "")); code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d %v", code, body)
	}
}

// TestOrderAttribution (ADR-0043): an order shows the campaign that
// brought it; one without has no attribution.
func TestOrderAttribution(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, owner := f.storeA, f.tok[authz.RoleOwner]
	shopper := tenant.WithStore(t.Context(), a)
	p, err := f.env.Svc.PlaceOrder(shopper, domain.NewOrder{Source: domain.SourceStorefront, Name: "nn", Phone: "01012345671",
		Governorate: "EG-C", City: "cc", Address: "aaaaa", Lines: []domain.LineInput{{VariantID: f.variantA, Qty: 1}},
		Attribution: domain.Attribution{Source: "tiktok", Campaign: "eid"}, Signals: domain.Signals{IP: "203.0.113.9"}})
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ := f.do(t, req(t, http.MethodGet, path(a, "/"+p.Order.ID.String()), owner, ""))
	o, _ := body["order"].(map[string]any)
	at, _ := o["attribution"].(map[string]any)
	if code != http.StatusOK || at["source"] != "tiktok" || at["campaign"] != "eid" || len(at) != 2 {
		t.Fatalf("getOrder attribution = %d %v", code, o["attribution"])
	}
	if strings.Contains(fmt.Sprint(body), "203.0.113.9") {
		t.Fatal("the order shows its signals")
	}
	code, list, _ := f.do(t, req(t, http.MethodGet, path(a, ""), owner, ""))
	items, _ := list["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["attribution"] == nil {
		t.Fatalf("listOrders = %d %v", code, list)
	}
	plain, err := f.env.Svc.PlaceOrder(shopper, domain.NewOrder{Source: domain.SourceStorefront, Name: "nn", Phone: "01012345672",
		Governorate: "EG-C", City: "cc", Address: "aaaaa", Lines: []domain.LineInput{{VariantID: f.variantA, Qty: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body, _ = f.do(t, req(t, http.MethodGet, path(a, "/"+plain.Order.ID.String()), owner, "")); code != http.StatusOK ||
		body["order"].(map[string]any)["attribution"] != nil {
		t.Fatalf("an order without a campaign: %d %v", code, body["order"])
	}
}
