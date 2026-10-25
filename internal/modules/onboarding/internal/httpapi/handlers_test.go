package httpapi_test

import (
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

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
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

// TestMain fails the package if any onboarding operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "onboarding")); len(missing) > 0 {
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
	jobs   *testkit.Jobs
	storeA tenant.StoreID
	storeB tenant.StoreID
	ctxB   tenant.StoreID
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
	jobs := testkit.NewJobs()
	svc := app.NewService(app.Deps{DB: pool, Jobs: jobs, Templates: nil})
	mk := func() tenant.StoreID {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			if _, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "onh-"+strings.ToLower(id.String()[14:])); execErr != nil {
				return execErr
			}
			return svc.StartTx(sctx, tx)
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	a, b := mk(), mk()
	// Store B has chosen its template: what A must never see or change.
	if _, err = svc.Choose(tenant.WithStore(ctx, b), domain.Choice{Activity: domain.Perfume, Look: domain.Soft, Color: "#123456"}); err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermStoreRead},
		authz.RoleShipping: {authz.PermOrdersRead},
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
	return fixture{h: mux, svc: svc, jobs: jobs, storeA: a, storeB: b, ctxB: b}
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
	return "/v1/stores/" + s.String() + "/onboarding" + rest
}

func template(m map[string]any) map[string]any {
	t, _ := m["template"].(map[string]any)
	return t
}

const choice = `{"activity":"clothing","look":"modern","color":"#0F2E52"}`

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, o := f.do(t, req(t, http.MethodGet, path(a, ""), tokSupportA, ""))
	if code != http.StatusOK || o["status"] != "in_progress" || template(o)["status"] != "none" || o["activity"] != nil {
		t.Fatalf("getOnboarding = %d %v", code, o)
	}
	code, o = f.do(t, req(t, http.MethodPut, path(a, "/template"), tokOwnerA, choice))
	if code != http.StatusAccepted || o["activity"] != "clothing" || o["look"] != "modern" || o["color"] != "#0f2e52" || template(o)["status"] != "preparing" {
		t.Fatalf("chooseOnboardingTemplate = %d %v", code, o)
	}
	code, o = f.do(t, req(t, http.MethodPost, path(a, "/complete"), tokOwnerA, ""))
	if code != http.StatusOK || o["status"] != "completed" || o["completed_at"] == nil {
		t.Fatalf("completeOnboarding = %d %v", code, o)
	}
	code, o = f.do(t, req(t, http.MethodPut, path(a, "/template"), tokOwnerA, choice))
	if code != http.StatusConflict || o["code"] != "onboarding_completed" {
		t.Fatalf("choose after closing = %d %v", code, o)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cases := []struct{ name, body string }{
		{"unknown activity", `{"activity":"cars","look":"modern","color":"#000000"}`},
		{"unknown look", `{"activity":"food","look":"loud","color":"#000000"}`},
		{"bad colour", `{"activity":"food","look":"soft","color":"red"}`},
		{"missing fields", `{}`},
		{"not json", `{`},
	}
	for _, c := range cases {
		code, o := f.do(t, req(t, http.MethodPut, path(f.storeA, "/template"), tokOwnerA, c.body))
		if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v", c.name, code, o)
		}
	}
	if len(f.jobs.Args) != 1 { // store B's, from the fixture
		t.Fatalf("jobs enqueued for invalid input: %v", f.jobs.Kinds())
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	cases := []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"anonymous read", http.MethodGet, path(a, ""), "", "", http.StatusUnauthorized},
		{"support reads", http.MethodGet, path(a, ""), tokSupportA, "", http.StatusOK},
		{"support cannot choose", http.MethodPut, path(a, "/template"), tokSupportA, choice, http.StatusForbidden},
		{"support cannot close", http.MethodPost, path(a, "/complete"), tokSupportA, "", http.StatusForbidden},
		{"shipping cannot read", http.MethodGet, path(a, ""), tokShippingA, "", http.StatusForbidden},
	}
	for _, c := range cases {
		if code, o := f.do(t, req(t, c.method, c.path, c.token, c.body)); code != c.want {
			t.Errorf("%s: %d %v; want %d", c.name, code, o, c.want)
		}
	}
	if code, o := f.do(t, req(t, http.MethodGet, path(a, ""), tokOwnerA, "")); code != http.StatusOK || o["status"] != "in_progress" {
		t.Fatalf("store A's wizard changed: %d %v", code, o)
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.storeB
	leaks := []string{"perfume", "#123456"}
	unchanged := func(t testing.TB) {
		t.Helper()
		st, err := f.svc.Get(tenant.WithStore(t.Context(), f.ctxB))
		if err != nil || st.Status() != domain.InProgress || st.Choice == nil || st.Choice.Activity != domain.Perfume || st.Seq != 1 {
			t.Fatalf("store B's wizard changed: %+v %v", st, err)
		}
	}
	cases := []testkit.CrossTenantCase{
		{OperationID: "getOnboarding", Request: req(t, http.MethodGet, path(b, ""), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "chooseOnboardingTemplate", Request: req(t, http.MethodPut, path(b, "/template"), tokOwnerA, choice), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "completeOnboarding", Request: req(t, http.MethodPost, path(b, "/complete"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
}
