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

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/httpapi"
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

// TestMain fails the package if an entitlements operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "entitlements")); len(missing) > 0 {
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
	tokOwnerB   = "owner-b"
)

type fixture struct {
	h              http.Handler
	storeA, storeB tenant.StoreID
}

// products counts by store: B holds 44 products, every other store 12
// (B's number must never reach A).
func products(b tenant.StoreID) app.Counter {
	return func(ctx context.Context, _ pgx.Tx) (int64, error) {
		id, err := tenant.FromContext(ctx)
		if id == b {
			return 44, err
		}
		return 12, err
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	f := fixture{}
	mk := func(prefix string) tenant.StoreID {
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, prefix+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	f.storeA, f.storeB = mk("eua-"), mk("eub-")
	svc := app.NewService(app.Deps{DB: pool, Source: app.Fixed{domain.OrdersPerMonth: {Value: 30}, domain.Products: {Unlimited: true}},
		Counters: map[domain.Feature]app.Counter{domain.Products: products(f.storeB)}})
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   authz.Permissions(),
		authz.RoleManager: {authz.PermOrdersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokManagerA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleManager}},
		tokOwnerB:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = mux
	return f
}

func get(t *testing.T, h http.Handler, s tenant.StoreID, token string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/stores/"+s.String()+"/usage", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestGetUsage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	code, body := get(t, f.h, f.storeA, tokOwnerA)
	items, _ := body["items"].([]any)
	// Monthly meters always; counted ones where the module counts (here
	// products only).
	if code != http.StatusOK || len(items) != 3 || body["ad_free"] != false {
		t.Fatalf("usage = %d %v", code, body)
	}
	orders, prods := items[0].(map[string]any), items[2].(map[string]any)
	if orders["feature"] != "orders_per_month" || orders["kind"] != "monthly" || orders["limit"] != float64(30) || orders["used"] != float64(0) ||
		prods["feature"] != "products" || prods["unlimited"] != true || prods["limit"] != nil || prods["used"] != float64(12) {
		t.Fatalf("meters = %v", items)
	}
	if code, _ = get(t, f.h, f.storeA, tokManagerA); code != http.StatusForbidden {
		t.Fatalf("manager = %d", code)
	}
}

func TestUsageCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/stores/"+f.storeB.String()+"/usage", nil)
	r.Header.Set("Authorization", "Bearer "+tokOwnerA)
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{OperationID: "getUsage", Request: r, Leaks: []string{"44"}})
}
