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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
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

// TestMain fails the package if any promotions operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "promotions")); len(missing) > 0 {
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
	tokOwnerA   = "owner-a"
	tokManagerA = "manager-a"
	tokSupportA = "support-a"
	tokOwnerB   = "owner-b"
	codeOfB     = "SECRETB"
)

type fixture struct {
	h              http.Handler
	svc            *app.Service
	pool           *pgxpool.Pool
	ids            *ulid.Generator
	storeA, storeB tenant.StoreID
	ctxA, ctxB     context.Context
	couponB        domain.Coupon
	usedB          domain.Coupon
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := fixture{pool: pool, ids: ulid.NewGenerator()}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: f.ids})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: f.ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "prh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	f.ctxA, f.storeA = mk()
	f.ctxB, f.storeB = mk()
	if f.couponB, err = f.svc.Create(f.ctxB, domain.Coupon{Code: codeOfB, Kind: domain.Percent, Percent: 30, Active: true}); err != nil {
		t.Fatal(err)
	}
	if f.usedB, err = f.svc.Create(f.ctxB, domain.Coupon{Code: "USEDB", Kind: domain.FreeShipping, Active: true}); err != nil {
		t.Fatal(err)
	}
	f.redeem(f.ctxB, t, "USEDB")

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   authz.Permissions(),
		authz.RoleManager: {authz.PermPromotionsManage},
		authz.RoleSupport: {authz.PermOrdersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: f.ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokManagerA: {UserID: f.ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleManager}},
		tokSupportA: {UserID: f.ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokOwnerB:   {UserID: f.ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, f.svc, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = mux
	return f
}

// redeem has an order take code in the store of ctx.
func (f fixture) redeem(ctx context.Context, t *testing.T, code string) {
	t.Helper()
	sub, err := money.New(10000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, rErr := f.svc.RedeemTx(ctx, tx, code, sub, f.ids.New(), f.ids.New())
		return rErr
	}); err != nil {
		t.Fatal(err)
	}
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

// sameInstant compares a returned timestamp with want, whatever zone it
// is written in.
func sameInstant(t *testing.T, got any, want string) bool {
	t.Helper()
	s, _ := got.(string)
	g, err1 := time.Parse(time.RFC3339Nano, s)
	w, err2 := time.Parse(time.RFC3339Nano, want)
	return err1 == nil && err2 == nil && g.Equal(w)
}

func path(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/coupons" + rest
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	body := `{"code":"eid-10","kind":"percent","percent":10,"min_subtotal_minor":20000,"max_uses":100,"max_per_customer":1,
		"starts_at":"2026-11-01T00:00:00Z","ends_at":"2026-11-30T00:00:00Z","active":true}`
	code, c := f.do(t, req(t, http.MethodPost, path(a, ""), tokManagerA, body))
	if code != http.StatusCreated || c["code"] != "EID-10" || c["percent"] != float64(10) || c["uses"] != float64(0) || c["amount_minor"] != nil ||
		c["max_per_customer"] != float64(1) || !sameInstant(t, c["ends_at"], "2026-11-30T00:00:00Z") {
		t.Fatalf("createCoupon = %d %v", code, c)
	}
	id := c["id"].(string)
	if code, c = f.do(t, req(t, http.MethodGet, path(a, "/"+id), tokOwnerA, "")); code != http.StatusOK || c["code"] != "EID-10" {
		t.Fatalf("getCoupon = %d %v", code, c)
	}
	update := `{"code":"EID-10","kind":"fixed","amount_minor":5000,"active":false}`
	code, c = f.do(t, req(t, http.MethodPut, path(a, "/"+id), tokManagerA, update))
	if code != http.StatusOK || c["kind"] != "fixed" || c["amount_minor"] != float64(5000) || c["percent"] != nil || c["active"] != false ||
		c["max_uses"] != nil || c["min_subtotal_minor"] != float64(0) {
		t.Fatalf("updateCoupon = %d %v", code, c)
	}
	code, list := f.do(t, req(t, http.MethodGet, path(a, ""), tokOwnerA, ""))
	if code != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("listCoupons = %d %v", code, list)
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, path(a, "/"+id), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deleteCoupon = %d", code)
	}
	if code, _ = f.do(t, req(t, http.MethodGet, path(a, "/"+id), tokOwnerA, "")); code != http.StatusNotFound {
		t.Fatalf("after delete = %d", code)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	if code, _ := f.do(t, req(t, http.MethodPost, path(a, ""), tokOwnerA, `{"code":"TAKEN","kind":"free_shipping","active":true}`)); code != http.StatusCreated {
		t.Fatal(code)
	}
	f.redeem(f.ctxA, t, "TAKEN")
	var taken string
	_, list := f.do(t, req(t, http.MethodGet, path(a, ""), tokOwnerA, ""))
	taken = list["items"].([]any)[0].(map[string]any)["id"].(string)
	tests := []struct {
		name, method, path, body string
		want                     int
		field                    string
	}{
		{"short code", http.MethodPost, "", `{"code":"a","kind":"percent","percent":5,"active":true}`, http.StatusUnprocessableEntity, "code"},
		{"bad kind", http.MethodPost, "", `{"code":"ABC","kind":"bogo","active":true}`, http.StatusUnprocessableEntity, "kind"},
		{"percent over 100", http.MethodPost, "", `{"code":"ABC","kind":"percent","percent":120,"active":true}`, http.StatusUnprocessableEntity, "percent"},
		{"fixed without amount", http.MethodPost, "", `{"code":"ABC","kind":"fixed","active":true}`, http.StatusUnprocessableEntity, "amount_minor"},
		{"ends before start", http.MethodPost, "", `{"code":"ABC","kind":"free_shipping","active":true,"starts_at":"2026-11-02T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"}`,
			http.StatusUnprocessableEntity, "ends_at"},
		{"code taken", http.MethodPost, "", `{"code":"taken","kind":"free_shipping","active":true}`, http.StatusConflict, ""},
		{"no body", http.MethodPost, "", ``, http.StatusBadRequest, ""},
		{"code of a used coupon", http.MethodPut, "/" + taken, `{"code":"OTHER","kind":"free_shipping","active":true}`, http.StatusConflict, ""},
		{"delete a used coupon", http.MethodDelete, "/" + taken, ``, http.StatusConflict, ""},
		{"update without body", http.MethodPut, "/" + taken, ``, http.StatusBadRequest, ""},
		{"malformed id", http.MethodGet, "/nope", ``, http.StatusNotFound, ""},
		{"malformed id update", http.MethodPut, "/nope", `{"code":"ABC","kind":"free_shipping","active":true}`, http.StatusNotFound, ""},
		{"malformed id delete", http.MethodDelete, "/nope", ``, http.StatusNotFound, ""},
		{"unknown id", http.MethodGet, "/" + ulid.NewGenerator().New().String(), ``, http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := f.do(t, req(t, tt.method, path(a, tt.path), tokOwnerA, tt.body))
			if code != tt.want {
				t.Fatalf("%s %s = %d %v, want %d", tt.method, tt.path, code, body, tt.want)
			}
			if tt.field != "" && !strings.Contains(fmt.Sprint(body["errors"]), "field:"+tt.field+" ") {
				t.Fatalf("errors = %v, want one on %s", body["errors"], tt.field)
			}
		})
	}
}

// TestAuthorization: the owner and manager manage coupons; support does
// not; no token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	body := `{"code":"ABC","kind":"free_shipping","active":true}`
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokSupportA, http.MethodGet, "", ``, http.StatusForbidden},
		{tokSupportA, http.MethodPost, "", body, http.StatusForbidden},
		{tokSupportA, http.MethodGet, "/" + id, ``, http.StatusForbidden},
		{tokSupportA, http.MethodPut, "/" + id, body, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, "/" + id, ``, http.StatusForbidden},
		{"", http.MethodGet, "", ``, http.StatusUnauthorized},
		{tokManagerA, http.MethodGet, "", ``, http.StatusOK},
	}
	for _, tt := range tests {
		if code, b := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, b, tt.want)
		}
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	cid, uid := f.couponB.ID.String(), f.usedB.ID.String()
	leaks := []string{cid, uid, codeOfB, "USEDB"}
	unchanged := func(t testing.TB) {
		t.Helper()
		list, err := f.svc.Coupons(f.ctxB)
		if err != nil || len(list) != 2 {
			t.Fatalf("store B's coupons changed: %+v %v", list, err)
		}
		c, err := f.svc.Coupon(f.ctxB, f.couponB.ID)
		if err != nil || c.Code != codeOfB || c.Percent != 30 || !c.Active {
			t.Fatalf("store B's coupon changed: %+v %v", c, err)
		}
	}
	body := `{"code":"HIJACK","kind":"percent","percent":100,"active":true}`
	cases := []testkit.CrossTenantCase{
		{OperationID: "listCoupons", Request: req(t, http.MethodGet, path(b, ""), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "createCoupon", Request: req(t, http.MethodPost, path(b, ""), tokOwnerA, body), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getCoupon", Request: req(t, http.MethodGet, path(b, "/"+cid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateCoupon", Request: req(t, http.MethodPut, path(b, "/"+cid), tokOwnerA, body), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteCoupon", Request: req(t, http.MethodDelete, path(b, "/"+cid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		// A's store in the path, B's coupon IDs.
		{OperationID: "getCoupon", Request: req(t, http.MethodGet, path(a, "/"+cid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateCoupon", Request: req(t, http.MethodPut, path(a, "/"+cid), tokOwnerA, body), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteCoupon", Request: req(t, http.MethodDelete, path(a, "/"+cid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteCoupon", Request: req(t, http.MethodDelete, path(a, "/"+uid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.Method+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// A may use B's code for its own coupon: codes are per store.
	if code, c := f.do(t, req(t, http.MethodPost, path(a, ""), tokOwnerA, `{"code":"`+codeOfB+`","kind":"free_shipping","active":true}`)); code != http.StatusCreated {
		t.Fatalf("same code in A = %d %v", code, c)
	}
	if code, list := f.do(t, req(t, http.MethodGet, path(b, ""), tokOwnerB, "")); code != http.StatusOK || len(list["items"].([]any)) != 2 {
		t.Fatalf("B's coupons = %d %v", code, list)
	}
}
