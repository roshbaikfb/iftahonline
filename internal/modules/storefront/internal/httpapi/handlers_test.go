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

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
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

// TestMain fails the package if any storefront operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "storefront")); len(missing) > 0 {
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
	tokOwnerB   = "owner-b"
)

type fixture struct {
	h       http.Handler
	svc     *app.Service
	storeA  tenant.StoreID
	storeB  tenant.StoreID
	ctxB    context.Context
	landing domain.Page // store B's
	homeB   domain.Page
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg, err := sections.New()
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Sections: reg})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "sfh-"+strings.ToLower(id.String()[14:]))
			return execErr
		})
		if txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	_, a := mk()
	ctxB, b := mk()

	// Store B's secrets, which store A must never see or change.
	if _, err = svc.UpdateTheme(ctxB, domain.ThemeSettings{Colors: domain.Colors{Primary: "#b0b0b1", Accent: "#b0b0b2", Background: "#ffffff", Text: "#000000"},
		Font: domain.FontNotoKufi, Radius: domain.RadiusSmall}, 0); err != nil {
		t.Fatal(err)
	}
	homeB, err := svc.SaveHome(ctxB, domain.PageInput{Title: "B secret home", Sections: []domain.Section{
		{ID: "b-secret-hero", Type: domain.SectionHero, Settings: json.RawMessage(`{"heading":"B secret heading"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := svc.CreateLandingPage(ctxB, domain.PageInput{Title: "B secret offer", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SavePolicy(ctxB, domain.PolicyRefund, domain.PageInput{Body: "B secret refund terms", Status: "published"}); err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   authz.Permissions(),
		authz.RoleSupport: {authz.PermCatalogRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleOwner}},
		tokSupportA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleSupport}},
		tokOwnerB:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{b: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, reg, pol, testkit.FakeStoreScope(resp, members), resp)
	return fixture{h: mux, svc: svc, storeA: a, storeB: b, ctxB: ctxB, landing: landing, homeB: homeB}
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

func storePath(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/storefront" + rest
}

const themeBody = `{"version":%d,"settings":{"colors":{"primary":"#0F766E","accent":"#f59e0b","background":"#ffffff","text":"#111827"},"font":"tajawal","radius":"large"}}`

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, th := f.do(t, req(t, http.MethodGet, storePath(a, "/theme"), tokOwnerA, ""))
	if code != http.StatusOK || th["version"] != float64(0) || th["settings"].(map[string]any)["font"] != "cairo" || th["updated_at"] != nil {
		t.Fatalf("getStorefrontTheme = %d %v", code, th)
	}
	code, th = f.do(t, req(t, http.MethodPut, storePath(a, "/theme"), tokOwnerA, fmt.Sprintf(themeBody, 0)))
	colors := th["settings"].(map[string]any)["colors"].(map[string]any)
	if code != http.StatusOK || th["version"] != float64(1) || colors["primary"] != "#0f766e" || th["updated_at"] == nil {
		t.Fatalf("updateStorefrontTheme = %d %v", code, th)
	}

	code, st := f.do(t, req(t, http.MethodGet, storePath(a, "/section-types"), tokOwnerA, ""))
	items, _ := st["items"].([]any)
	if code != http.StatusOK || len(items) != 7 || st["definitions"] == nil {
		t.Fatalf("listSectionTypes = %d %v", code, st)
	}
	if first := items[0].(map[string]any); first["type"] != "hero" || first["schema"].(map[string]any)["title"] != "Hero" {
		t.Fatalf("first section type %v", first)
	}

	code, home := f.do(t, req(t, http.MethodGet, storePath(a, "/home"), tokOwnerA, ""))
	if code != http.StatusOK || home["id"] != nil || home["version"] != float64(0) || len(home["sections"].([]any)) != 3 {
		t.Fatalf("getHomePage default = %d %v", code, home)
	}
	homeBody := `{"title":"متجري","version":0,"sections":[{"type":"hero","settings":{"heading":"مرحبًا","button_label":"تسوق","button_link":"/c/new"}},{"id":"grid","type":"product_grid","settings":{"source":"newest","limit":12}}]}`
	code, home = f.do(t, req(t, http.MethodPut, storePath(a, "/home"), tokOwnerA, homeBody))
	secs, _ := home["sections"].([]any)
	if code != http.StatusOK || home["id"] == nil || home["version"] != float64(1) || len(secs) != 2 || home["status"] != "published" {
		t.Fatalf("saveHomePage = %d %v", code, home)
	}
	if s0 := secs[0].(map[string]any); s0["id"] == "" || s0["settings"].(map[string]any)["heading"] != "مرحبًا" {
		t.Fatalf("saved section %v", s0)
	}

	code, lp := f.do(t, req(t, http.MethodPost, storePath(a, "/pages"), tokOwnerA, `{"title":"عرض الصيف","status":"published","sections":[{"type":"faq","settings":{"items":[{"question":"س","answer":"ج"}]}}]}`))
	if code != http.StatusCreated || lp["slug"] != "عرض-الصيف" || lp["kind"] != "landing" || lp["version"] != float64(1) {
		t.Fatalf("createLandingPage = %d %v", code, lp)
	}
	lid := lp["id"].(string)
	if c, list := f.do(t, req(t, http.MethodGet, storePath(a, "/pages"), tokOwnerA, "")); c != http.StatusOK || len(list["items"].([]any)) != 1 ||
		len(list["items"].([]any)[0].(map[string]any)["sections"].([]any)) != 0 {
		t.Fatalf("listLandingPages = %d %v", c, list)
	}
	if c, got := f.do(t, req(t, http.MethodGet, storePath(a, "/pages/"+lid), tokOwnerA, "")); c != http.StatusOK || len(got["sections"].([]any)) != 1 {
		t.Fatalf("getLandingPage = %d %v", c, got)
	}
	if c, got := f.do(t, req(t, http.MethodPut, storePath(a, "/pages/"+lid), tokOwnerA, `{"title":"Summer","slug":"summer","version":1}`)); c != http.StatusOK || got["slug"] != "summer" || got["status"] != "draft" || got["version"] != float64(2) {
		t.Fatalf("updateLandingPage = %d %v", c, got)
	}
	if c, _ := f.do(t, req(t, http.MethodDelete, storePath(a, "/pages/"+lid), tokOwnerA, "")); c != http.StatusNoContent {
		t.Fatalf("deleteLandingPage = %d", c)
	}

	code, pols := f.do(t, req(t, http.MethodGet, storePath(a, "/policies"), tokOwnerA, ""))
	if code != http.StatusOK || len(pols["items"].([]any)) != 4 || pols["items"].([]any)[0].(map[string]any)["slug"] != "shipping" {
		t.Fatalf("listPolicies = %d %v", code, pols)
	}
	code, pol := f.do(t, req(t, http.MethodPut, storePath(a, "/policies/refund"), tokOwnerA, `{"title":"الاسترجاع","body":"خلال ١٤ يومًا.\n\nبشرط سلامة المنتج.","status":"published"}`))
	if code != http.StatusOK || pol["kind"] != "policy" || pol["slug"] != "refund" || pol["version"] != float64(1) || !strings.Contains(pol["body"].(string), "\n\n") {
		t.Fatalf("savePolicy = %d %v", code, pol)
	}
	if c, got := f.do(t, req(t, http.MethodGet, storePath(a, "/policies/refund"), tokOwnerA, "")); c != http.StatusOK || got["title"] != "الاسترجاع" {
		t.Fatalf("getPolicy = %d %v", c, got)
	}
}

func problemFields(m map[string]any) []string {
	var out []string
	errsList, _ := m["errors"].([]any)
	for _, e := range errsList {
		fe := e.(map[string]any)
		out = append(out, fmt.Sprint(fe["field"], ":", fe["code"]))
	}
	return out
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	tests := []struct {
		name   string
		rq     *http.Request
		status int
		fields []string
	}{
		{"low contrast", req(t, http.MethodPut, storePath(a, "/theme"), tokOwnerA,
			`{"version":0,"settings":{"colors":{"primary":"#000000","accent":"#000000","background":"#ffffff","text":"#eeeeee"},"font":"cairo","radius":"none"}}`),
			http.StatusUnprocessableEntity, []string{"colors.text:low_contrast"}},
		{"unknown font", req(t, http.MethodPut, storePath(a, "/theme"), tokOwnerA,
			`{"version":0,"settings":{"colors":{"primary":"#000000","accent":"#000000","background":"#ffffff","text":"#000000"},"font":"https://evil/font.woff","radius":"none"}}`),
			http.StatusUnprocessableEntity, []string{"font:invalid"}},
		{"theme without body", req(t, http.MethodPut, storePath(a, "/theme"), tokOwnerA, ""), http.StatusBadRequest, nil},
		{"malformed JSON", req(t, http.MethodPost, storePath(a, "/pages"), tokOwnerA, `{"title":`), http.StatusBadRequest, nil},
		{"section settings", req(t, http.MethodPut, storePath(a, "/home"), tokOwnerA,
			`{"sections":[{"type":"reviews","settings":{"items":[{"name":"","rating":9,"text":"x"}]}},{"type":"hero","settings":{"onclick":"x"}}]}`),
			http.StatusUnprocessableEntity, []string{"sections[0].settings.items[0].name:minLength", "sections[0].settings.items[0].rating:maximum", "sections[1].settings.onclick:unknown"}},
		{"unknown section type", req(t, http.MethodPut, storePath(a, "/home"), tokOwnerA, `{"sections":[{"type":"html","settings":{"html":"<script>"}}]}`),
			http.StatusUnprocessableEntity, []string{"sections[0].type:invalid"}},
		{"landing without title", req(t, http.MethodPost, storePath(a, "/pages"), tokOwnerA, `{}`),
			http.StatusUnprocessableEntity, []string{"title:length", "slug:required"}},
		{"policy with sections", req(t, http.MethodPut, storePath(a, "/policies/terms"), tokOwnerA, `{"sections":[{"type":"faq"}]}`),
			http.StatusUnprocessableEntity, []string{"sections:not_allowed"}},
		{"unknown policy", req(t, http.MethodGet, storePath(a, "/policies/cookies"), tokOwnerA, ""), http.StatusNotFound, nil},
		{"malformed page ID", req(t, http.MethodGet, storePath(a, "/pages/not-an-id"), tokOwnerA, ""), http.StatusNotFound, nil},
		{"stale theme", req(t, http.MethodPut, storePath(a, "/theme"), tokOwnerA, fmt.Sprintf(themeBody, 7)), http.StatusConflict, nil},
		{"stale home", req(t, http.MethodPut, storePath(a, "/home"), tokOwnerA, `{"version":3}`), http.StatusConflict, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, body := f.do(t, tt.rq)
			if code != tt.status {
				t.Fatalf("status = %d, want %d: %v", code, tt.status, body)
			}
			got := problemFields(body)
			for _, want := range tt.fields {
				if !strings.Contains(strings.Join(got, " "), want) {
					t.Fatalf("fields %q, want %q", got, tt.fields)
				}
			}
			if len(got) != len(tt.fields) {
				t.Fatalf("fields %q, want %q", got, tt.fields)
			}
		})
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	type op struct{ method, path, body string }
	ops := []op{
		{http.MethodGet, "/theme", ""}, {http.MethodPut, "/theme", fmt.Sprintf(themeBody, 0)},
		{http.MethodGet, "/section-types", ""},
		{http.MethodGet, "/home", ""}, {http.MethodPut, "/home", `{}`},
		{http.MethodGet, "/pages", ""}, {http.MethodPost, "/pages", `{"title":"x"}`},
		{http.MethodGet, "/pages/" + id, ""}, {http.MethodPut, "/pages/" + id, `{"title":"x"}`}, {http.MethodDelete, "/pages/" + id, ""},
		{http.MethodGet, "/policies", ""}, {http.MethodGet, "/policies/refund", ""}, {http.MethodPut, "/policies/refund", `{}`},
	}
	for _, o := range ops {
		t.Run(o.method+o.path, func(t *testing.T) {
			t.Parallel()
			if c, _ := f.do(t, req(t, o.method, storePath(a, o.path), "", o.body)); c != http.StatusUnauthorized {
				t.Fatalf("no session = %d", c)
			}
			if c, _ := f.do(t, req(t, o.method, storePath(a, o.path), "forged", o.body)); c != http.StatusUnauthorized {
				t.Fatalf("unknown token = %d", c)
			}
			if c, _ := f.do(t, req(t, o.method, storePath(a, o.path), tokSupportA, o.body)); c != http.StatusForbidden {
				t.Fatalf("support (no storefront.manage) = %d", c)
			}
		})
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	lid, hid := f.landing.ID.String(), f.homeB.ID.String()
	leaks := []string{"B secret", "b-secret-hero", "#b0b0b1", "noto-kufi-arabic", lid, hid}

	unchanged := func(tb testing.TB) {
		tb.Helper()
		ctx := f.ctxB
		if th, err := f.svc.GetTheme(ctx); err != nil || th.Version != 1 || th.Settings.Colors.Primary != "#b0b0b1" {
			tb.Errorf("store B's theme changed: %+v %v", th, err)
		}
		if h, err := f.svc.GetHome(ctx); err != nil || h.Version != 1 || h.Title != "B secret home" {
			tb.Errorf("store B's home changed: %+v %v", h, err)
		}
		if lp, err := f.svc.GetLandingPage(ctx, f.landing.ID); err != nil || lp.Version != 1 || lp.Title != "B secret offer" {
			tb.Errorf("store B's landing page changed: %+v %v", lp, err)
		}
		if list, err := f.svc.ListLandingPages(ctx); err != nil || len(list) != 1 {
			tb.Errorf("store B's landing pages changed: %d %v", len(list), err)
		}
		if p, err := f.svc.GetPolicy(ctx, domain.PolicyRefund); err != nil || p.Version != 1 || p.Body != "B secret refund terms" {
			tb.Errorf("store B's policy changed: %+v %v", p, err)
		}
	}
	page := `{"title":"pwned","version":1}`

	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "getStorefrontTheme", Request: req(t, http.MethodGet, storePath(b, "/theme"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateStorefrontTheme", Request: req(t, http.MethodPut, storePath(b, "/theme"), tokOwnerA, fmt.Sprintf(themeBody, 1)), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listSectionTypes", Request: req(t, http.MethodGet, storePath(b, "/section-types"), tokOwnerA, "")},
		{OperationID: "getHomePage", Request: req(t, http.MethodGet, storePath(b, "/home"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "saveHomePage", Request: req(t, http.MethodPut, storePath(b, "/home"), tokOwnerA, page), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listLandingPages", Request: req(t, http.MethodGet, storePath(b, "/pages"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "createLandingPage", Request: req(t, http.MethodPost, storePath(b, "/pages"), tokOwnerA, page), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getLandingPage", Request: req(t, http.MethodGet, storePath(b, "/pages/"+lid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateLandingPage", Request: req(t, http.MethodPut, storePath(b, "/pages/"+lid), tokOwnerA, page), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteLandingPage", Request: req(t, http.MethodDelete, storePath(b, "/pages/"+lid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listPolicies", Request: req(t, http.MethodGet, storePath(b, "/policies"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getPolicy", Request: req(t, http.MethodGet, storePath(b, "/policies/refund"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "savePolicy", Request: req(t, http.MethodPut, storePath(b, "/policies/refund"), tokOwnerA, page), Leaks: leaks, Unchanged: unchanged},
		// A's store, B's page IDs.
		{OperationID: "getLandingPage", Request: req(t, http.MethodGet, storePath(a, "/pages/"+lid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateLandingPage", Request: req(t, http.MethodPut, storePath(a, "/pages/"+lid), tokOwnerA, page), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteLandingPage", Request: req(t, http.MethodDelete, storePath(a, "/pages/"+lid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getLandingPage", Request: req(t, http.MethodGet, storePath(a, "/pages/"+hid), tokOwnerA, ""), Leaks: leaks},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// Store A's own reads after all that show only A's defaults.
	if code, home := f.do(t, req(t, http.MethodGet, storePath(a, "/home"), tokOwnerA, "")); code != http.StatusOK || home["version"] != float64(0) {
		t.Fatalf("store A's home = %d %v", code, home)
	}
}
