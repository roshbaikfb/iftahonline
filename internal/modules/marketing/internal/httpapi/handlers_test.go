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

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	infra       testinfra.Infra
	crossTenant testkit.Registry
)

// TestMain fails the package if any marketing operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "marketing")); len(missing) > 0 {
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
	metaOfB     = "987654321098765"
	tokenOfB    = "EAAB-store-b-secret"
)

type fixture struct {
	h              http.Handler
	svc            *app.Service
	storeA, storeB tenant.StoreID
	ctxB           context.Context
	feedOfB        string // store B's feed key
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	f := fixture{svc: app.NewService(app.Deps{DB: pool, IDs: ids, Sealer: sealer})}
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "mkh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	_, f.storeA = mk()
	f.ctxB, f.storeB = mk()
	token := tokenOfB
	if _, err = f.svc.SavePixel(f.ctxB, domain.Meta, domain.PixelInput{PixelID: metaOfB, Enabled: true, ServerToken: &token}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.SaveSettings(f.ctxB, domain.Settings{Consent: domain.ConsentRequired}); err != nil {
		t.Fatal(err)
	}
	if f.feedOfB, err = f.svc.RenewFeedKey(f.ctxB); err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   authz.Permissions(),
		authz.RoleManager: {authz.PermMarketingManage},
		authz.RoleSupport: {authz.PermOrdersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokManagerA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleManager}},
		tokSupportA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokOwnerB:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
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

func path(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/marketing" + rest
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, list, _ := f.do(t, req(t, http.MethodGet, path(a, "/pixels"), tokManagerA, ""))
	if code != http.StatusOK || len(list["items"].([]any)) != 0 || len(list["providers"].([]any)) != 5 {
		t.Fatalf("listPixels = %d %v", code, list)
	}
	ads := list["providers"].([]any)[3].(map[string]any)
	if ads["provider"] != "google_ads" || ads["label"] != true || ads["server"] != false {
		t.Fatalf("google_ads provider %v", ads)
	}

	body := `{"pixel_id":"123456789012345","enabled":true,"test_code":"TEST1","server_token":"EAAB-store-a-secret","version":0}`
	code, p, raw := f.do(t, req(t, http.MethodPut, path(a, "/pixels/meta"), tokManagerA, body))
	if code != http.StatusOK || p["pixel_id"] != "123456789012345" || p["has_server_token"] != true || p["version"] != float64(1) ||
		p["test_code"] != "TEST1" || strings.Contains(raw, "secret") {
		t.Fatalf("savePixel = %d %s", code, raw)
	}
	body = `{"pixel_id":"123456789012345","enabled":false,"version":1}`
	if code, p, _ = f.do(t, req(t, http.MethodPut, path(a, "/pixels/meta"), tokOwnerA, body)); code != http.StatusOK || p["enabled"] != false || p["has_server_token"] != true {
		t.Fatalf("savePixel keeps the token = %d %v", code, p)
	}
	if code, list, raw = f.do(t, req(t, http.MethodGet, path(a, "/pixels"), tokOwnerA, "")); code != http.StatusOK || len(list["items"].([]any)) != 1 || strings.Contains(raw, "secret") {
		t.Fatalf("listPixels = %d %s", code, raw)
	}
	if code, _, _ = f.do(t, req(t, http.MethodDelete, path(a, "/pixels/meta"), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deletePixel = %d", code)
	}

	code, s, _ := f.do(t, req(t, http.MethodGet, path(a, "/settings"), tokManagerA, ""))
	if code != http.StatusOK || s["consent"] != "auto" || s["version"] != float64(0) {
		t.Fatalf("getMarketingSettings = %d %v", code, s)
	}
	if code, s, _ = f.do(t, req(t, http.MethodPut, path(a, "/settings"), tokManagerA, `{"consent":"required","version":0}`)); code != http.StatusOK ||
		s["consent"] != "required" || s["version"] != float64(1) {
		t.Fatalf("saveMarketingSettings = %d %v", code, s)
	}

	if code, s, _ = f.do(t, req(t, http.MethodGet, path(a, "/feed"), tokManagerA, "")); code != http.StatusOK || s["enabled"] != false || len(s) != 1 {
		t.Fatalf("getProductFeed off = %d %v", code, s)
	}
	code, s, _ = f.do(t, req(t, http.MethodPost, path(a, "/feed"), tokManagerA, ""))
	key, _ := s["key"].(string)
	if code != http.StatusOK || s["enabled"] != true || len(key) != 32 || s["google_path"] != "/feeds/google.xml?key="+key || s["meta_path"] != "/feeds/meta.xml?key="+key {
		t.Fatalf("renewProductFeedKey = %d %v", code, s)
	}
	if code, s, _ = f.do(t, req(t, http.MethodGet, path(a, "/feed"), tokOwnerA, "")); code != http.StatusOK || s["key"] != key {
		t.Fatalf("getProductFeed on = %d %v", code, s)
	}
	if code, s, _ = f.do(t, req(t, http.MethodPost, path(a, "/feed"), tokOwnerA, "")); code != http.StatusOK || s["key"] == key {
		t.Fatalf("renewProductFeedKey again = %d %v", code, s)
	}
	for range 2 {
		if code, _, _ = f.do(t, req(t, http.MethodDelete, path(a, "/feed"), tokOwnerA, "")); code != http.StatusNoContent {
			t.Fatalf("disableProductFeed = %d", code)
		}
	}
	if code, s, _ = f.do(t, req(t, http.MethodGet, path(a, "/feed"), tokOwnerA, "")); code != http.StatusOK || s["enabled"] != false {
		t.Fatalf("getProductFeed after disable = %d %v", code, s)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	tests := []struct {
		name, method, path, body string
		want                     int
		field                    string
	}{
		{"bad pixel id", http.MethodPut, "/pixels/meta", `{"pixel_id":"abc","enabled":true,"version":0}`, http.StatusUnprocessableEntity, "pixel_id"},
		{"ads without label", http.MethodPut, "/pixels/google_ads", `{"pixel_id":"AW-123456789","enabled":true,"version":0}`, http.StatusUnprocessableEntity, "label"},
		{"token for ads", http.MethodPut, "/pixels/google_ads", `{"pixel_id":"AW-123456789","label":"AbC-123_xyz","server_token":"abcdefghij","enabled":true,"version":0}`,
			http.StatusUnprocessableEntity, "server_token"},
		{"unknown provider", http.MethodPut, "/pixels/gtm", `{"pixel_id":"GTM-ABCD","enabled":true,"version":0}`, http.StatusNotFound, ""},
		{"unknown provider delete", http.MethodDelete, "/pixels/gtm", ``, http.StatusNotFound, ""},
		{"nothing to delete", http.MethodDelete, "/pixels/snap", ``, http.StatusNotFound, ""},
		{"stale version", http.MethodPut, "/pixels/tiktok", `{"pixel_id":"C4ABCDEF1234567890AB","enabled":true,"version":4}`, http.StatusConflict, ""},
		{"no body", http.MethodPut, "/pixels/meta", ``, http.StatusBadRequest, ""},
		{"bad consent", http.MethodPut, "/settings", `{"consent":"always","version":0}`, http.StatusUnprocessableEntity, "consent"},
		{"stale settings", http.MethodPut, "/settings", `{"consent":"off","version":3}`, http.StatusConflict, ""},
		{"settings without body", http.MethodPut, "/settings", ``, http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := f.do(t, req(t, tt.method, path(a, tt.path), tokOwnerA, tt.body))
			if code != tt.want {
				t.Fatalf("%s %s = %d %v, want %d", tt.method, tt.path, code, body, tt.want)
			}
			if tt.field != "" && !strings.Contains(fmt.Sprint(body["errors"]), "field:"+tt.field+" ") {
				t.Fatalf("errors = %v, want one on %s", body["errors"], tt.field)
			}
		})
	}
}

// TestAuthorization: the owner and manager manage the pixels; support
// does not; no token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	body := `{"pixel_id":"123456789012345","enabled":true,"version":0}`
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokSupportA, http.MethodGet, "/pixels", ``, http.StatusForbidden},
		{tokSupportA, http.MethodPut, "/pixels/meta", body, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, "/pixels/meta", ``, http.StatusForbidden},
		{tokSupportA, http.MethodGet, "/settings", ``, http.StatusForbidden},
		{tokSupportA, http.MethodPut, "/settings", `{"consent":"off","version":0}`, http.StatusForbidden},
		{tokSupportA, http.MethodGet, "/feed", ``, http.StatusForbidden},
		{tokSupportA, http.MethodPost, "/feed", ``, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, "/feed", ``, http.StatusForbidden},
		{"", http.MethodPost, "/feed", ``, http.StatusUnauthorized},
		{"", http.MethodGet, "/pixels", ``, http.StatusUnauthorized},
		{tokManagerA, http.MethodGet, "/settings", ``, http.StatusOK},
	}
	for _, tt := range tests {
		if code, b, _ := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, b, tt.want)
		}
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.storeB
	leaks := []string{metaOfB, tokenOfB, `"required"`, f.feedOfB}
	unchanged := func(t testing.TB) {
		t.Helper()
		pixels, err := f.svc.Pixels(f.ctxB)
		if err != nil || len(pixels) != 1 || pixels[0].PixelID != metaOfB || !pixels[0].Enabled || !pixels[0].HasServerToken || pixels[0].Version != 1 {
			t.Fatalf("store B's pixels changed: %+v %v", pixels, err)
		}
		s, err := f.svc.Settings(f.ctxB)
		if err != nil || s.Consent != domain.ConsentRequired || s.Version != 1 {
			t.Fatalf("store B's settings changed: %+v %v", s, err)
		}
		if key, ok, err := f.svc.FeedKey(f.ctxB); err != nil || !ok || key != f.feedOfB {
			t.Fatalf("store B's feed changed: %q %v %v", key, ok, err)
		}
	}
	pixel := `{"pixel_id":"111111111111111","enabled":false,"server_token":"","version":1}`
	cases := []testkit.CrossTenantCase{
		{OperationID: "listPixels", Request: req(t, http.MethodGet, path(b, "/pixels"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "savePixel", Request: req(t, http.MethodPut, path(b, "/pixels/meta"), tokOwnerA, pixel), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deletePixel", Request: req(t, http.MethodDelete, path(b, "/pixels/meta"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getMarketingSettings", Request: req(t, http.MethodGet, path(b, "/settings"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "saveMarketingSettings", Request: req(t, http.MethodPut, path(b, "/settings"), tokOwnerA, `{"consent":"off","version":1}`),
			Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getProductFeed", Request: req(t, http.MethodGet, path(b, "/feed"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "renewProductFeedKey", Request: req(t, http.MethodPost, path(b, "/feed"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "disableProductFeed", Request: req(t, http.MethodDelete, path(b, "/feed"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.Method+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// A's own store with B's version: A has no pixel there, so B's row is
	// never reached (404 for the update of a pixel A does not have).
	if code, body, _ := f.do(t, req(t, http.MethodPut, path(f.storeA, "/pixels/meta"), tokOwnerA, pixel)); code != http.StatusConflict {
		t.Fatalf("A updating a pixel it does not have = %d %v", code, body)
	}
	unchanged(t)
	if code, list, _ := f.do(t, req(t, http.MethodGet, path(b, "/pixels"), tokOwnerB, "")); code != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("B's pixels = %d %v", code, list)
	}
}
