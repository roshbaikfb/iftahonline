package httpapi_test

import (
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
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/risktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
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

// TestMain fails the package if any risk operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "risk")); len(missing) > 0 {
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

const (
	tokOwnerA    = "owner-a"
	tokShippingA = "shipping-a"
	tokOwnerB    = "owner-b"
	tokNoPermA   = "noperm-a"
)

// fixture: one customer (phone) with orders in stores A and B. B's
// refusals make them high everywhere; A must see the band and only its
// own order.
type fixture struct {
	h              http.Handler
	svc            *app.Service
	orders         *risktest.Orders
	storeA, storeB tenant.StoreID
	ctxA, ctxB     context.Context
	phone          phone.E164
	orderA         orders.Order // delivered, in A
	refusedB       orders.Order // in B
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	f := fixture{orders: risktest.NewOrders(ids, time.Now().UTC())}
	keyer, err := domain.NewKeyer([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	f.svc = app.NewService(app.Deps{DB: pool, Orders: f.orders, Keyer: keyer, Jobs: testkit.NewJobs()})
	f.ctxA, f.storeA = risktest.Store(t, pool, ids)
	f.ctxB, f.storeB = risktest.Store(t, pool, ids)
	ctxC, _ := risktest.Store(t, pool, ids)
	f.phone = risktest.Phone(t, ids)

	learn := func(ctx context.Context, o orders.Order) {
		t.Helper()
		if lErr := f.svc.Learn(ctx, o.ID); lErr != nil {
			t.Fatal(lErr)
		}
	}
	f.orderA = f.orders.Order(f.ctxA, f.phone, 2, orders.StatusDelivered, orders.ReasonNone)
	learn(f.ctxA, f.orderA)
	f.refusedB = f.orders.Order(f.ctxB, f.phone, 3, orders.StatusCancelled, orders.ReasonRefused)
	learn(f.ctxB, f.refusedB)
	learn(f.ctxB, f.orders.Order(f.ctxB, f.phone, 4, orders.StatusReturned, orders.ReasonRefused))
	learn(ctxC, f.orders.Order(ctxC, f.phone, 5, orders.StatusFailedDelivery, orders.ReasonNoAnswer))

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleShipping: {authz.PermOrdersRead},
		authz.RoleSupport:  {authz.PermCustomersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokShippingA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleShipping}},
		tokNoPermA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokOwnerB:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, f.svc, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = mux
	return f
}

func req(t *testing.T, method, path, token, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rq := httptest.NewRequestWithContext(t.Context(), method, path, r)
	if token != "" {
		rq.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		rq.Header.Set("Content-Type", "application/json")
	}
	return rq
}

func (f fixture) do(t *testing.T, rq *http.Request) (int, map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, m, rec.Body.String()
}

func riskPath(s tenant.StoreID, order ulid.ID) string {
	return "/v1/stores/" + s.String() + "/orders/" + order.String() + "/risk"
}

func bandsPath(s tenant.StoreID) string { return "/v1/stores/" + s.String() + "/risk/bands" }

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	code, r, _ := f.do(t, req(t, http.MethodGet, riskPath(f.storeA, f.orderA.ID), tokShippingA, ""))
	h, _ := r["history"].(map[string]any)
	if code != http.StatusOK || r["band"] != "high" || h["orders"] != float64(1) || h["delivered"] != float64(1) ||
		h["refused"] != float64(0) || h["no_answer"] != float64(0) || h["last_outcome_at"] == nil {
		t.Fatalf("getOrderRisk = %d %v", code, r)
	}
	unknown := ulid.NewGenerator().New()
	body := fmt.Sprintf(`{"order_ids":[%q,%q]}`, f.orderA.ID, unknown)
	code, b, _ := f.do(t, req(t, http.MethodPost, bandsPath(f.storeA), tokOwnerA, body))
	bands, _ := b["bands"].([]any)
	if code != http.StatusOK || len(bands) != 1 || bands[0].(map[string]any)["order_id"] != f.orderA.ID.String() ||
		bands[0].(map[string]any)["band"] != "high" {
		t.Fatalf("listRiskBands = %d %v", code, b)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, id := f.storeA, f.orderA.ID.String()
	many := make([]string, app.MaxBands+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", ulid.NewGenerator().New())
	}
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"malformed order", http.MethodGet, "/v1/stores/" + a.String() + "/orders/nope/risk", "", http.StatusNotFound},
		{"unknown order", http.MethodGet, riskPath(a, ulid.NewGenerator().New()), "", http.StatusNotFound},
		{"no body", http.MethodPost, bandsPath(a), "", http.StatusBadRequest},
		{"bad json", http.MethodPost, bandsPath(a), "{", http.StatusBadRequest},
		{"no orders", http.MethodPost, bandsPath(a), `{"order_ids":[]}`, http.StatusUnprocessableEntity},
		{"too many orders", http.MethodPost, bandsPath(a), `{"order_ids":[` + strings.Join(many, ",") + `]}`, http.StatusUnprocessableEntity},
		{"bad id", http.MethodPost, bandsPath(a), `{"order_ids":["x"]}`, http.StatusUnprocessableEntity},
		{"duplicate id", http.MethodPost, bandsPath(a), fmt.Sprintf(`{"order_ids":[%q,%q]}`, id, id), http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, _, body := f.do(t, req(t, tt.method, tt.path, tokOwnerA, tt.body)); code != tt.want {
				t.Fatalf("%d, want %d: %s", code, tt.want, body)
			}
		})
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := fmt.Sprintf(`{"order_ids":[%q]}`, f.orderA.ID)
	for _, tt := range []struct {
		token, method, path, body string
		want                      int
	}{
		{"", http.MethodGet, riskPath(f.storeA, f.orderA.ID), "", http.StatusUnauthorized},
		{tokNoPermA, http.MethodGet, riskPath(f.storeA, f.orderA.ID), "", http.StatusForbidden},
		{tokNoPermA, http.MethodPost, bandsPath(f.storeA), body, http.StatusForbidden},
		{tokShippingA, http.MethodPost, bandsPath(f.storeA), body, http.StatusOK},
	} {
		if code, _, b := f.do(t, req(t, tt.method, tt.path, tt.token, tt.body)); code != tt.want {
			t.Errorf("%q %s %s = %d, want %d: %s", tt.token, tt.method, tt.path, code, tt.want, b)
		}
	}
}

// TestCrossTenant: store A's member never reaches B's orders, through B's
// store path or with B's order IDs in A's.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	leaks := []string{f.refusedB.ID.String(), f.storeB.String(), f.phone.Unmasked()}
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{
		OperationID: "getOrderRisk",
		Request:     req(t, http.MethodGet, riskPath(f.storeA, f.refusedB.ID), tokOwnerA, ""),
		Leaks:       leaks,
	})
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{
		OperationID: "getOrderRisk",
		Request:     req(t, http.MethodGet, riskPath(f.storeB, f.refusedB.ID), tokOwnerA, ""),
		Leaks:       leaks,
	})
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{
		OperationID: "listRiskBands",
		Request:     req(t, http.MethodPost, bandsPath(f.storeB), tokOwnerA, fmt.Sprintf(`{"order_ids":[%q]}`, f.refusedB.ID)),
		Leaks:       leaks,
		Unchanged: func(t testing.TB) { // a read sent as POST: B's history is what it was
			r, err := f.svc.ForOrder(f.ctxB, f.refusedB.ID)
			if err != nil || r.History.Orders != 2 || r.History.Outcomes[domain.Refused] != 2 {
				t.Fatalf("store B's history = %+v, %v", r, err)
			}
		},
	})
	// B's order IDs in A's own path are simply left out.
	code, b, _ := f.do(t, req(t, http.MethodPost, bandsPath(f.storeA), tokOwnerA, fmt.Sprintf(`{"order_ids":[%q]}`, f.refusedB.ID)))
	if code != http.StatusOK || len(b["bands"].([]any)) != 0 {
		t.Fatalf("A asking for B's order = %d %v", code, b)
	}
}

// TestPrivacy is P8's acceptance: whatever store A asks, its answers hold
// nothing of another store's orders: no ID, no count, no date, no reason.
// The customer has three orders elsewhere; A sees its band (which they
// made high) and its own one order.
func TestPrivacy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, _, detail := f.do(t, req(t, http.MethodGet, riskPath(f.storeA, f.orderA.ID), tokOwnerA, ""))
	_, _, list := f.do(t, req(t, http.MethodPost, bandsPath(f.storeA), tokOwnerA,
		fmt.Sprintf(`{"order_ids":[%q,%q]}`, f.orderA.ID, f.refusedB.ID)))
	for name, body := range map[string]string{"getOrderRisk": detail, "listRiskBands": list} {
		var anyValue map[string]any
		if err := json.Unmarshal([]byte(body), &anyValue); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{f.refusedB.ID.String(), f.storeB.String(), f.phone.Unmasked(), f.phone.Masked()} {
			if strings.Contains(body, secret) {
				t.Errorf("%s holds %q: %s", name, secret, body)
			}
		}
		// Only the band and A's own history, with the documented keys.
		for _, key := range []string{"store", "stores", "score", "evidence", "signals", "orders_elsewhere", "platform"} {
			if strings.Contains(body, `"`+key+`"`) {
				t.Errorf("%s has a %q field: %s", name, key, body)
			}
		}
	}
	var d struct {
		Band    string         `json:"band"`
		History map[string]any `json:"history"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatal(err)
	}
	if d.Band != "high" {
		t.Fatalf("band = %q", d.Band)
	}
	for k, v := range d.History {
		switch k {
		case "orders", "delivered":
			if v != float64(1) {
				t.Errorf("%s = %v, want A's own 1", k, v)
			}
		case "returned", "refused", "no_answer", "unconfirmed":
			if v != float64(0) {
				t.Errorf("%s = %v: another store's outcomes leaked", k, v)
			}
		case "last_outcome_at":
			if at, err := time.Parse(time.RFC3339Nano, v.(string)); err != nil || !at.Equal(f.orderA.StatusChangedAt.Truncate(time.Microsecond)) {
				t.Errorf("last_outcome_at = %v, want A's own %v", v, f.orderA.StatusChangedAt)
			}
		default:
			t.Errorf("unexpected history field %q", k)
		}
	}
}
