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

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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

// TestMain fails the package if any analytics operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "analytics")); len(missing) > 0 {
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
	tokSupportA = "support-a"
	tokFreeA    = "owner-free"
)

// noProfit is a plan without the dashboard.
type noProfit struct{ entitlements.Entitlements }

func (noProfit) Require(_ context.Context, f entitlements.Feature) error {
	if f == entitlements.Profit {
		return errs.Forbidden("not in the plan")
	}
	return nil
}

type fixture struct {
	h                      http.Handler
	svc                    *app.Service
	storeA, storeB, storeF tenant.StoreID
	ctxB                   context.Context
	spendB                 string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	read, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	ids := ulid.NewGenerator()
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	deps := app.Deps{DB: pool, Read: postgres.ReadOnly(read), IDs: ids,
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: "EGP", Location: cairo}, nil
		}}
	svc := app.NewService(deps)
	deps.Plan = noProfit{}
	free := app.NewService(deps)
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "anh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return ctx, id
	}
	f := fixture{svc: svc}
	_, f.storeA = mk()
	f.ctxB, f.storeB = mk()
	_, f.storeF = mk()
	s, err := svc.AddAdSpend(f.ctxB, app.AdSpendInput{Day: time.Now().AddDate(0, 0, -1).UTC().Truncate(24 * time.Hour), Platform: "meta",
		Campaign: "secret-b", AmountMinor: 77700})
	if err != nil {
		t.Fatal(err)
	}
	f.spendB = s.ID.String()

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   authz.Permissions(),
		authz.RoleSupport: {authz.PermOrdersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokSupportA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokFreeA:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeF: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, pol, testkit.FakeStoreScope(resp, members), resp)
	freeMux := http.NewServeMux()
	httpapi.Mount(freeMux, free, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, f.storeF.String()) {
			freeMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
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

func path(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/analytics" + rest
}

func yesterday() string { return time.Now().AddDate(0, 0, -1).Format("2006-01-02") }

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	if code, s, _ := f.do(t, req(t, http.MethodGet, path(a, "/settings"), tokOwnerA, "")); code != http.StatusOK || s["cod_fee_bp"] != float64(0) {
		t.Fatalf("getProfitSettings = %d %v", code, s)
	}
	code, s, _ := f.do(t, req(t, http.MethodPut, path(a, "/settings"), tokOwnerA, `{"delivery_cost_minor":4500,"cod_fee_bp":150}`))
	if code != http.StatusOK || s["delivery_cost_minor"] != float64(4500) || s["cod_fee_bp"] != float64(150) || s["return_cost_minor"] != nil {
		t.Fatalf("updateProfitSettings = %d %v", code, s)
	}
	code, sp, _ := f.do(t, req(t, http.MethodPost, path(a, "/ad-spend"), tokOwnerA,
		fmt.Sprintf(`{"day":%q,"platform":"tiktok","campaign":"eid","amount_minor":12000}`, yesterday())))
	if code != http.StatusCreated || sp["campaign"] != "eid" || sp["source"] != "manual" || sp["currency"] != "EGP" {
		t.Fatalf("createAdSpend = %d %v", code, sp)
	}
	code, list, _ := f.do(t, req(t, http.MethodGet, path(a, "/ad-spend?from="+yesterday()+"&to="+yesterday()), tokOwnerA, ""))
	if code != http.StatusOK || len(list["items"].([]any)) != 1 || list["count"] != float64(1) || list["total_minor"] != sp["amount_minor"] {
		t.Fatalf("listAdSpend = %d %v", code, list)
	}
	if _, err := f.svc.Rollup(tenant.WithStore(t.Context(), a)); err != nil {
		t.Fatal(err)
	}
	code, r, _ := f.do(t, req(t, http.MethodGet, path(a, "/profit?from="+yesterday()+"&to="+yesterday()+"&by=campaign"), tokOwnerA, ""))
	totals, _ := r["totals"].(map[string]any)
	rows, _ := r["rows"].([]any)
	if code != http.StatusOK || r["currency"] != "EGP" || r["by"] != "campaign" || totals["ad_spend_minor"] != float64(12000) ||
		totals["profit_minor"] != float64(-12000) || len(rows) != 1 || rows[0].(map[string]any)["key"] != "eid" {
		t.Fatalf("getProfit = %d %v", code, r)
	}
	if code, _, _ = f.do(t, req(t, http.MethodDelete, path(a, "/ad-spend/"+sp["id"].(string)), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deleteAdSpend = %d", code)
	}
	if code, _, _ = f.do(t, req(t, http.MethodGet, path(a, "/profit?from="+yesterday()+"&to="+yesterday()), tokOwnerA, "")); code != http.StatusOK {
		t.Fatalf("getProfit by default = %d", code)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	for _, tt := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"no range", http.MethodGet, "/profit", "", http.StatusBadRequest},
		{"reversed range", http.MethodGet, "/profit?from=2026-10-02&to=2026-10-01", "", http.StatusUnprocessableEntity},
		{"too long", http.MethodGet, "/profit?from=2024-01-01&to=2026-10-01", "", http.StatusUnprocessableEntity},
		{"bad by", http.MethodGet, "/profit?from=2026-10-01&to=2026-10-02&by=total", "", http.StatusUnprocessableEntity},
		{"fee over 100%", http.MethodPut, "/settings", `{"cod_fee_bp":10001}`, http.StatusUnprocessableEntity},
		{"no settings body", http.MethodPut, "/settings", ``, http.StatusBadRequest},
		{"future spend", http.MethodPost, "/ad-spend", `{"day":"2099-01-01","platform":"meta","amount_minor":1}`, http.StatusUnprocessableEntity},
		{"bad platform", http.MethodPost, "/ad-spend", `{"day":"` + yesterday() + `","platform":"myspace","amount_minor":1}`, http.StatusUnprocessableEntity},
		{"no spend body", http.MethodPost, "/ad-spend", ``, http.StatusBadRequest},
		{"unknown spend", http.MethodDelete, "/ad-spend/" + ulid.NewGenerator().New().String(), "", http.StatusNotFound},
		{"malformed spend", http.MethodDelete, "/ad-spend/nope", "", http.StatusNotFound},
	} {
		if code, _, b := f.do(t, req(t, tt.method, path(a, tt.path), tokOwnerA, tt.body)); code != tt.want {
			t.Errorf("%s = %d, want %d: %s", tt.name, code, tt.want, b)
		}
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	for _, tt := range []struct {
		token, method, path, body string
		want                      int
	}{
		{"", http.MethodGet, path(a, "/settings"), "", http.StatusUnauthorized},
		{tokSupportA, http.MethodGet, path(a, "/profit?from=2026-10-01&to=2026-10-02"), "", http.StatusForbidden},
		{tokSupportA, http.MethodGet, path(a, "/settings"), "", http.StatusForbidden},
		{tokSupportA, http.MethodPut, path(a, "/settings"), `{"cod_fee_bp":1}`, http.StatusForbidden},
		{tokSupportA, http.MethodGet, path(a, "/ad-spend?from=2026-10-01&to=2026-10-02"), "", http.StatusForbidden},
		{tokSupportA, http.MethodPost, path(a, "/ad-spend"), `{"day":"` + yesterday() + `","platform":"meta","amount_minor":1}`, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, path(a, "/ad-spend/"+f.spendB), "", http.StatusForbidden},
		// A plan without the dashboard (ADR-0069): no figures; the rest works.
		{tokFreeA, http.MethodGet, path(f.storeF, "/profit?from=2026-10-01&to=2026-10-02"), "", http.StatusForbidden},
		{tokFreeA, http.MethodGet, path(f.storeF, "/settings"), "", http.StatusOK},
	} {
		if code, _, b := f.do(t, req(t, tt.method, tt.path, tt.token, tt.body)); code != tt.want {
			t.Errorf("%q %s %s = %d, want %d: %s", tt.token, tt.method, tt.path, code, tt.want, b)
		}
	}
}

// TestCrossTenant: store A's owner never reads or changes B's figures,
// settings or ad spend.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.storeB
	leaks := []string{f.spendB, "secret-b", "77700"}
	unchanged := func(t testing.TB) {
		list, err := f.svc.AdSpend(f.ctxB, time.Now().AddDate(0, 0, -3), time.Now().AddDate(0, 0, 1))
		if err != nil || len(list.Items) != 1 || list.Items[0].Amount != 77700 {
			t.Fatalf("store B's spend changed: %+v, %v", list, err)
		}
		s, err := f.svc.Settings(f.ctxB)
		if err != nil || s.CODFeeBP != 0 {
			t.Fatalf("store B's settings changed: %+v, %v", s, err)
		}
	}
	day := yesterday()
	cases := []testkit.CrossTenantCase{
		{OperationID: "getProfit", Request: req(t, http.MethodGet, path(b, "/profit?from="+day+"&to="+day), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getProfitSettings", Request: req(t, http.MethodGet, path(b, "/settings"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateProfitSettings", Request: req(t, http.MethodPut, path(b, "/settings"), tokOwnerA, `{"cod_fee_bp":900}`),
			Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listAdSpend", Request: req(t, http.MethodGet, path(b, "/ad-spend?from="+day+"&to="+day), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "createAdSpend", Request: req(t, http.MethodPost, path(b, "/ad-spend"), tokOwnerA,
			`{"day":"`+day+`","platform":"meta","amount_minor":1}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteAdSpend", Request: req(t, http.MethodDelete, path(b, "/ad-spend/"+f.spendB), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		// B's entry through A's own store.
		{OperationID: "deleteAdSpend", Request: req(t, http.MethodDelete, path(f.storeA, "/ad-spend/"+f.spendB), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
	}
	// A's own list never shows B's entry.
	if code, list, body := f.do(t, req(t, http.MethodGet, path(f.storeA, "/ad-spend?from="+day+"&to="+day), tokOwnerA, "")); code != http.StatusOK ||
		len(list["items"].([]any)) != 0 || strings.Contains(body, "secret-b") {
		t.Fatalf("A's list = %d %s", code, body)
	}
}
