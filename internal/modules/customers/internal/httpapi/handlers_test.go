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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
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

// TestMain fails the package if any customers operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "customers")); len(missing) > 0 {
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
	tokSupportA  = "support-a"
	tokShippingA = "shipping-a"
	tokOwnerB    = "owner-b"
)

type fixture struct {
	h      http.Handler
	svc    *app.Service
	storeA tenant.StoreID
	storeB tenant.StoreID
	ctxA   context.Context
	ctxB   context.Context
	custB  domain.Customer // store B's customer, with an order
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	svc := app.NewService(app.Deps{DB: pool, IDs: ids,
		Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "cuh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	ctxA, a := mk()
	ctxB, b := mk()
	var custB domain.Customer
	if err = postgres.InTenantTx(ctxB, pool, func(tx pgx.Tx) (txErr error) {
		custB, txErr = svc.RecordOrderTx(ctxB, tx, mustPhone(t, "01055555555"), "B secret name")
		return txErr
	}); err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermCustomersRead},
		authz.RoleShipping: {authz.PermCatalogRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleOwner}},
		tokSupportA:  {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleSupport}},
		tokShippingA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleShipping}},
		tokOwnerB:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{b: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, pol, testkit.FakeStoreScope(resp, members), resp)
	return fixture{h: mux, svc: svc, storeA: a, storeB: b, ctxA: ctxA, ctxB: ctxB, custB: custB}
}

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
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

func (f fixture) do(t *testing.T, rq *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, m
}

func path(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/customers" + rest
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, c := f.do(t, req(t, http.MethodPost, path(a, "/blocks"), tokOwnerA, `{"phone":"010 1234 5678","reason":"طلبات وهمية"}`))
	if code != http.StatusOK || c["phone"] != "+201012345678" || c["blocked"] != true || c["block_reason"] != "طلبات وهمية" ||
		c["orders_count"] != float64(0) || c["blocked_at"] == nil {
		t.Fatalf("blockPhone = %d %v", code, c)
	}
	id := c["id"].(string)

	code, page := f.do(t, req(t, http.MethodGet, path(a, "?blocked=true"), tokSupportA, ""))
	items, _ := page["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["id"] != id || page["next_cursor"] != nil {
		t.Fatalf("listCustomers blocked = %d %v", code, page)
	}
	if code, page = f.do(t, req(t, http.MethodGet, path(a, "?q=%2B201012345678"), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != 1 {
		t.Fatalf("listCustomers by phone = %d %v", code, page)
	}
	if c2, got := f.do(t, req(t, http.MethodGet, path(a, "/"+id), tokSupportA, "")); c2 != http.StatusOK || got["id"] != id {
		t.Fatalf("getCustomer = %d %v", c2, got)
	}
	if c2, got := f.do(t, req(t, http.MethodPost, path(a, "/"+id+"/unblock"), tokOwnerA, "")); c2 != http.StatusOK || got["blocked"] != false || got["blocked_at"] != nil {
		t.Fatalf("unblockCustomer = %d %v", c2, got)
	}
	if c2, got := f.do(t, req(t, http.MethodPost, path(a, "/"+id+"/block"), tokOwnerA, `{}`)); c2 != http.StatusOK || got["blocked"] != true {
		t.Fatalf("blockCustomer = %d %v", c2, got)
	}

	// Pagination over three customers, two per page.
	for _, p := range []string{"01000000001", "01000000002"} {
		if code, _ = f.do(t, req(t, http.MethodPost, path(a, "/blocks"), tokOwnerA, `{"phone":"`+p+`"}`)); code != http.StatusOK {
			t.Fatal(code)
		}
	}
	code, page = f.do(t, req(t, http.MethodGet, path(a, "?limit=2"), tokOwnerA, ""))
	next, _ := page["next_cursor"].(string)
	if code != http.StatusOK || len(page["items"].([]any)) != 2 || next == "" {
		t.Fatalf("page 1 = %d %v", code, page)
	}
	code, page = f.do(t, req(t, http.MethodGet, path(a, "?limit=2&cursor="+next), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("page 2 = %d %v", code, page)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"bad phone", http.MethodPost, "/blocks", `{"phone":"123"}`, http.StatusUnprocessableEntity},
		{"unsupported country", http.MethodPost, "/blocks", `{"phone":"+33612345678"}`, http.StatusUnprocessableEntity},
		{"missing phone", http.MethodPost, "/blocks", `{}`, http.StatusUnprocessableEntity},
		{"no body", http.MethodPost, "/blocks", ``, http.StatusBadRequest},
		{"bad json", http.MethodPost, "/blocks", `{"phone":`, http.StatusBadRequest},
		{"long reason", http.MethodPost, "/blocks", `{"phone":"01012345678","reason":"` + strings.Repeat("x", 301) + `"}`, http.StatusUnprocessableEntity},
		{"one-letter search", http.MethodGet, "?q=a", ``, http.StatusUnprocessableEntity},
		{"bad cursor", http.MethodGet, "?cursor=nope", ``, http.StatusUnprocessableEntity},
		{"limit too big", http.MethodGet, "?limit=101", ``, http.StatusUnprocessableEntity},
		{"bad blocked flag", http.MethodGet, "?blocked=maybe", ``, http.StatusBadRequest},
		{"malformed id", http.MethodGet, "/not-an-id", ``, http.StatusNotFound},
		{"unknown id", http.MethodPost, "/" + ulid.NewGenerator().New().String() + "/unblock", ``, http.StatusNotFound},
		{"block without body", http.MethodPost, "/" + f.custB.ID.String() + "/block", ``, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := f.do(t, req(t, tt.method, path(a, tt.path), tokOwnerA, tt.body))
			if code != tt.want {
				t.Fatalf("%s %s = %d %v, want %d", tt.method, tt.path, code, body, tt.want)
			}
		})
	}
}

// TestAuthorization: support reads but cannot change the block list in
// this policy; shipping cannot even read; no token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokSupportA, http.MethodPost, "/blocks", `{"phone":"01012345678"}`, http.StatusForbidden},
		{tokSupportA, http.MethodPost, "/" + id + "/block", `{}`, http.StatusForbidden},
		{tokSupportA, http.MethodPost, "/" + id + "/unblock", ``, http.StatusForbidden},
		{tokShippingA, http.MethodGet, "", ``, http.StatusForbidden},
		{tokShippingA, http.MethodGet, "/" + id, ``, http.StatusForbidden},
		{"", http.MethodGet, "", ``, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		if code, body := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, body, tt.want)
		}
	}
	// Nothing was blocked by the refused calls.
	if p, err := f.svc.List(f.ctxA, domain.Query{}); err != nil || len(p.Items) != 0 {
		t.Fatalf("store A customers after refused writes: %+v %v", p.Items, err)
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	cid := f.custB.ID.String()
	leaks := []string{cid, "B secret name", "+201055555555", "01055555555"}
	unchanged := func(t testing.TB) {
		t.Helper()
		got, err := f.svc.Get(f.ctxB, f.custB.ID)
		if err != nil || got.Blocked() || got.Name != "B secret name" || got.OrdersCount != 1 {
			t.Fatalf("store B's customer changed: %+v %v", got, err)
		}
		p, err := f.svc.List(f.ctxB, domain.Query{})
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("store B's customers: %+v %v", p.Items, err)
		}
	}
	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "listCustomers", Request: req(t, http.MethodGet, path(b, ""), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "blockPhone", Request: req(t, http.MethodPost, path(b, "/blocks"), tokOwnerA, `{"phone":"01055555555"}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getCustomer", Request: req(t, http.MethodGet, path(b, "/"+cid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "blockCustomer", Request: req(t, http.MethodPost, path(b, "/"+cid+"/block"), tokOwnerA, `{}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "unblockCustomer", Request: req(t, http.MethodPost, path(b, "/"+cid+"/unblock"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		// A's store in the path, B's customer ID.
		{OperationID: "getCustomer", Request: req(t, http.MethodGet, path(a, "/"+cid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "blockCustomer", Request: req(t, http.MethodPost, path(a, "/"+cid+"/block"), tokOwnerA, `{}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "unblockCustomer", Request: req(t, http.MethodPost, path(a, "/"+cid+"/unblock"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// Store A searching B's phone finds nothing, and B's block list
	// stays empty after A blocked the same number in A's own store.
	code, page := f.do(t, req(t, http.MethodGet, path(a, "?q=01055555555"), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("A searching B's phone = %d %v", code, page)
	}
	if code, _ = f.do(t, req(t, http.MethodPost, path(a, "/blocks"), tokOwnerA, `{"phone":"01055555555"}`)); code != http.StatusOK {
		t.Fatal(code)
	}
	unchanged(t)
}
