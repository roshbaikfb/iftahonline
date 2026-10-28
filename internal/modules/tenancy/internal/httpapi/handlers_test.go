package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
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

// TestMain fails the package if any tenancy operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "tenancy")); len(missing) > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "operations without a cross-tenant test:", missing)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

// runAll reports whether the whole package ran (no -run filter, no fuzz).
func runAll() bool {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.run=") || strings.HasPrefix(a, "-test.fuzz=") {
			return false
		}
	}
	return true
}

// testPolicy mirrors what identity's matrix will grant; it exercises the
// handlers' permission checks, not the matrix itself.
func testPolicy(t *testing.T) authz.Policy {
	t.Helper()
	p, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermStoreRead, authz.PermDomainsRead},
		authz.RoleShipping: {authz.PermStoreRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fixture struct {
	h      http.Handler
	svc    *app.Service
	owner  *pgxpool.Pool
	storeA tenant.StoreID
	storeB tenant.StoreID
	ctxA   context.Context
	ctxB   context.Context
	domB   domain.Domain // B's custom domain
	hostB  string
}

const (
	tokOwnerA    = "owner-a"
	tokSupportA  = "support-a"
	tokShippingA = "shipping-a"
	tokOwnerB    = "owner-b"
)

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, err := pgxpool.New(ctx, infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	svc := app.NewService(pool, ins, ids, "iftahonline.com", audit.Nop{})

	mk := func(prefix string) (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		slug := prefix + "-" + strings.ToLower(ids.New().String()[16:])
		txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, createErr := svc.CreateStoreTx(sctx, tx, domain.NewStore{Slug: slug, Name: "Store " + prefix, Currency: "EGP", Locale: "ar", Timezone: "Africa/Cairo"})
			return createErr
		})
		if txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	ctxA, a := mk("http-a")
	ctxB, b := mk("http-b")
	hostB := "b-" + strings.ToLower(ids.New().String()[16:]) + ".example.com"
	domB, err := svc.AddCustomDomain(ctxB, hostB)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	resp := problem.NewResponder(log)
	members := map[string]testkit.FakeMember{
		tokOwnerA:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleOwner}},
		tokSupportA:  {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleSupport}},
		tokShippingA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleShipping}},
		tokOwnerB:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{b: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, testEdge, testPolicy(t), testkit.FakeStoreScope(resp, members), resp)
	return fixture{h: mux, svc: svc, owner: owner, storeA: a, storeB: b, ctxA: ctxA, ctxB: ctxB, domB: domB, hostB: hostB}
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

func storePath(s tenant.StoreID, rest string) string { return "/v1/stores/" + s.String() + rest }

// testEdge is where the fixture's custom domains point (ADR-0037).
var testEdge = domain.Edge{ //nolint:gochecknoglobals // a fixed test value
	Target: "connect.example.test",
	IPv4:   []netip.Addr{netip.MustParseAddr("203.0.113.10")},
	IPv6:   []netip.Addr{netip.MustParseAddr("2001:db8::10")},
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, body := f.do(t, req(t, http.MethodGet, storePath(a, ""), tokOwnerA, ""))
	if code != http.StatusOK || body["id"] != a.String() || body["currency"] != "EGP" {
		t.Fatalf("getStore = %d %v", code, body)
	}
	code, body = f.do(t, req(t, http.MethodPatch, storePath(a, ""), tokOwnerA, `{"name":"اسم جديد","locale":"en","timezone":"Asia/Dubai"}`))
	if code != http.StatusOK || body["name"] != "اسم جديد" || body["locale"] != "en" {
		t.Fatalf("updateStore = %d %v", code, body)
	}

	code, body = f.do(t, req(t, http.MethodPost, storePath(a, "/domains"), tokOwnerA, `{"host":"Shop-A.example.com"}`))
	if code != http.StatusCreated || body["status"] != "pending" || body["host"] != "shop-a.example.com" {
		t.Fatalf("addDomain = %d %v", code, body)
	}
	ver, _ := body["verification"].(map[string]any)
	if ver["record_type"] != "TXT" || ver["name"] != "_iftah-verify.shop-a.example.com" || !strings.HasPrefix(ver["value"].(string), "iftah-verify=") {
		t.Fatalf("verification = %v", ver)
	}
	custom := body["id"].(string)
	// a subdomain points at the edge with one CNAME
	routing, _ := body["routing"].(map[string]any)
	if routing["apex"] != false || !sameRecords(routing["records"], [][3]string{{"CNAME", "shop-a.example.com", "connect.example.test"}}) {
		t.Fatalf("routing = %v", routing)
	}
	// an apex domain: an ALIAS, or the edge's addresses
	apex := "apex-" + strings.ToLower(f.storeA.String()[16:]) + ".com.eg"
	code, body = f.do(t, req(t, http.MethodPost, storePath(a, "/domains"), tokOwnerA, `{"host":"`+apex+`"}`))
	routing, _ = body["routing"].(map[string]any)
	if code != http.StatusCreated || routing["apex"] != true || !sameRecords(routing["records"], [][3]string{
		{"ALIAS", apex, "connect.example.test"}, {"A", apex, "203.0.113.10"}, {"AAAA", apex, "2001:db8::10"},
	}) {
		t.Fatalf("apex addDomain = %d %v", code, body)
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, storePath(a, "/domains/"+body["id"].(string)), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("delete apex = %d", code)
	}

	code, body = f.do(t, req(t, http.MethodGet, storePath(a, "/domains"), tokOwnerA, ""))
	items, _ := body["items"].([]any)
	if code != http.StatusOK || len(items) != 2 {
		t.Fatalf("listDomains = %d %v", code, body)
	}
	sub := items[0].(map[string]any)
	if sub["kind"] != "subdomain" || sub["is_primary"] != true || sub["verification"] != nil || sub["routing"] != nil {
		t.Fatalf("subdomain = %v", sub)
	}

	if code, body = f.do(t, req(t, http.MethodPost, storePath(a, "/domains/"+custom+"/verify"), tokOwnerA, "")); code != http.StatusAccepted || body["status"] != "pending" {
		t.Fatalf("verifyDomain = %d %v", code, body)
	}
	if code, body = f.do(t, req(t, http.MethodPost, storePath(a, "/domains/"+sub["id"].(string)+"/primary"), tokOwnerA, "")); code != http.StatusOK || body["is_primary"] != true {
		t.Fatalf("setPrimaryDomain = %d %v", code, body)
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, storePath(a, "/domains/"+custom), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deleteDomain = %d", code)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	tests := []struct {
		name   string
		rq     *http.Request
		status int
		code   string
	}{
		{"empty name", req(t, http.MethodPatch, storePath(a, ""), tokOwnerA, `{"name":"","locale":"ar","timezone":"UTC"}`), http.StatusUnprocessableEntity, "validation_failed"},
		{"unsupported timezone", req(t, http.MethodPatch, storePath(a, ""), tokOwnerA, `{"name":"x","locale":"ar","timezone":"Mars/Base"}`), http.StatusUnprocessableEntity, "validation_failed"},
		{"malformed json", req(t, http.MethodPatch, storePath(a, ""), tokOwnerA, `{"name":`), http.StatusBadRequest, ""},
		{"platform domain", req(t, http.MethodPost, storePath(a, "/domains"), tokOwnerA, `{"host":"x.iftahonline.com"}`), http.StatusUnprocessableEntity, "validation_failed"},
		{"not a domain", req(t, http.MethodPost, storePath(a, "/domains"), tokOwnerA, `{"host":"http://x.com/a"}`), http.StatusUnprocessableEntity, "validation_failed"},
		{"bad domain id", req(t, http.MethodDelete, storePath(a, "/domains/not-a-ulid"), tokOwnerA, ""), http.StatusNotFound, "domain_not_found"},
		{"missing domain", req(t, http.MethodPost, storePath(a, "/domains/"+ulid.NewGenerator().New().String()+"/verify"), tokOwnerA, ""), http.StatusNotFound, "domain_not_found"},
		{"bad store id", req(t, http.MethodGet, "/v1/stores/nope", tokOwnerA, ""), http.StatusNotFound, "store_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, body := f.do(t, tt.rq)
			if code != tt.status || (tt.code != "" && body["code"] != tt.code) {
				t.Fatalf("= %d %v; want %d %q", code, body, tt.status, tt.code)
			}
		})
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	tests := []struct {
		name   string
		rq     *http.Request
		status int
	}{
		{"no session", req(t, http.MethodGet, storePath(a, ""), "", ""), http.StatusUnauthorized},
		{"unknown token", req(t, http.MethodGet, storePath(a, ""), "forged", ""), http.StatusUnauthorized},
		{"support reads store", req(t, http.MethodGet, storePath(a, ""), tokSupportA, ""), http.StatusOK},
		{"support reads domains", req(t, http.MethodGet, storePath(a, "/domains"), tokSupportA, ""), http.StatusOK},
		{"support cannot update", req(t, http.MethodPatch, storePath(a, ""), tokSupportA, `{"name":"x","locale":"ar","timezone":"UTC"}`), http.StatusForbidden},
		{"support cannot add domain", req(t, http.MethodPost, storePath(a, "/domains"), tokSupportA, `{"host":"s.example.com"}`), http.StatusForbidden},
		{"shipping cannot read domains", req(t, http.MethodGet, storePath(a, "/domains"), tokShippingA, ""), http.StatusForbidden},
		{"shipping cannot delete", req(t, http.MethodDelete, storePath(a, "/domains/"+ulid.NewGenerator().New().String()), tokShippingA, ""), http.StatusForbidden},
		{"shipping cannot verify", req(t, http.MethodPost, storePath(a, "/domains/"+ulid.NewGenerator().New().String()+"/verify"), tokShippingA, ""), http.StatusForbidden},
		{"shipping cannot set primary", req(t, http.MethodPost, storePath(a, "/domains/"+ulid.NewGenerator().New().String()+"/primary"), tokShippingA, ""), http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, body := f.do(t, tt.rq); code != tt.status {
				t.Fatalf("= %d %v; want %d", code, body, tt.status)
			}
		})
	}
}

// TestCrossTenant: store A's owner reaches for store B, by B's store ID in
// the path and by B's domain ID under A's own path. Every attempt is a
// 404 that says nothing about B, and B's data is unchanged.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b, d := f.storeB, f.domB.ID.String()
	leaks := []string{f.hostB, f.domB.VerificationToken, "Store http-b"}

	unchanged := func(tb testing.TB) {
		tb.Helper()
		var name, status string
		var n int
		err := pgx.BeginFunc(tb.Context(), f.owner, func(tx pgx.Tx) error {
			if _, err := tx.Exec(tb.Context(), "SELECT set_config('app.store_id', $1, true)", b.ID); err != nil {
				return err
			}
			if err := tx.QueryRow(tb.Context(), "SELECT name FROM stores WHERE id = $1", b.ID).Scan(&name); err != nil {
				return err
			}
			if err := tx.QueryRow(tb.Context(), "SELECT status FROM store_domains WHERE id = $1", f.domB.ID).Scan(&status); err != nil {
				return err
			}
			return tx.QueryRow(tb.Context(), "SELECT count(*) FROM store_domains WHERE store_id = $1", b.ID).Scan(&n)
		})
		if err != nil || name != "Store http-b" || status != "pending" || n != 2 {
			tb.Errorf("store B changed: name=%q domain=%q domains=%d err=%v", name, status, n, err)
		}
	}

	cases := []testkit.CrossTenantCase{
		{OperationID: "getStore", Request: req(t, http.MethodGet, storePath(b, ""), tokOwnerA, "")},
		{OperationID: "updateStore", Request: req(t, http.MethodPatch, storePath(b, ""), tokOwnerA, `{"name":"pwned","locale":"ar","timezone":"UTC"}`), Unchanged: unchanged},
		{OperationID: "listDomains", Request: req(t, http.MethodGet, storePath(b, "/domains"), tokOwnerA, "")},
		{OperationID: "addDomain", Request: req(t, http.MethodPost, storePath(b, "/domains"), tokOwnerA, `{"host":"evil.example.com"}`), Unchanged: unchanged},
		{OperationID: "deleteDomain", Request: req(t, http.MethodDelete, storePath(b, "/domains/"+d), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "verifyDomain", Request: req(t, http.MethodPost, storePath(b, "/domains/"+d+"/verify"), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "setPrimaryDomain", Request: req(t, http.MethodPost, storePath(b, "/domains/"+d+"/primary"), tokOwnerA, ""), Unchanged: unchanged},
		// A's own store path, B's domain ID: the tenant filter and RLS hide it.
		{OperationID: "deleteDomain", Request: req(t, http.MethodDelete, storePath(f.storeA, "/domains/"+d), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "verifyDomain", Request: req(t, http.MethodPost, storePath(f.storeA, "/domains/"+d+"/verify"), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "setPrimaryDomain", Request: req(t, http.MethodPost, storePath(f.storeA, "/domains/"+d+"/primary"), tokOwnerA, ""), Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.URL.Path, func(t *testing.T) {
			c.Leaks = leaks
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}

	// B's owner still sees everything intact.
	if code, body := f.do(t, req(t, http.MethodGet, storePath(b, "/domains"), tokOwnerB, "")); code != http.StatusOK || len(body["items"].([]any)) != 2 {
		t.Fatalf("B's own view = %d %v", code, body)
	}
}

// sameRecords compares a routing's records with (type, name, value)s.
func sameRecords(got any, want [][3]string) bool {
	list, _ := got.([]any)
	if len(list) != len(want) {
		return false
	}
	for i, r := range list {
		m, _ := r.(map[string]any)
		if m["type"] != want[i][0] || m["name"] != want[i][1] || m["value"] != want[i][2] {
			return false
		}
	}
	return true
}
