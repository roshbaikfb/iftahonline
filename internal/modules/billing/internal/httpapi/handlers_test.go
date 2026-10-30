package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/httpapi"
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

// TestMain fails the package if any billing operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "billing")); len(missing) > 0 {
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

// pages is a checkout gateway that opens pages and charges nothing.
type pages struct{}

func (pages) Charge(context.Context, app.Charge) (app.ChargeResult, error) {
	return app.ChargeResult{}, nil
}
func (pages) Checkout(_ context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	return app.CheckoutPage{URL: "https://pay.example.com/" + c.Reference}, nil
}
func (pages) ParseCallback(url.Values, []byte) (app.Callback, error) {
	return app.Callback{}, app.ErrForgedCallback
}
func (pages) ReadCallback([]byte) (app.Callback, error) { return app.Callback{}, app.ErrBadCallback }
func (pages) Paid(context.Context, string, string, int64) (string, bool, error) {
	return "", false, nil
}

type fixture struct {
	h              http.Handler
	pool           *pgxpool.Pool
	storeA, storeB tenant.StoreID
	invoiceB       string
	numberB        string
}

// newFixture makes store A (no invoice) and store B with an open
// invoice, B's number kept for the leak checks.
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
	svc := app.NewService(app.Deps{DB: pool, Jobs: jobs, IDs: ids, Gateways: map[string]app.Gateway{"paymob": pages{}},
		PublicURL: "https://api.example.com", DashboardURL: "https://app.example.com",
		Stores: func(context.Context) (app.StoreInfo, error) { return app.StoreInfo{Name: "B", Currency: "EGP"}, nil }})
	f := fixture{pool: pool}
	mk := func(prefix string) (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			if _, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, prefix+strings.ToLower(id.String()[14:])); execErr != nil {
				return execErr
			}
			_, execErr := tx.Exec(sctx, `INSERT INTO subscriptions (store_id, plan_id, status, cycle, period_start, period_end)
				SELECT app.current_store_id(), id, 'active', 'monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z' FROM plans WHERE code = 'pro'`)
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	_, f.storeA = mk("bha-")
	ctxB, storeB := mk("bhb-")
	f.storeB = storeB
	if err = svc.InvoiceStore(ctxB, mustDate(t, "2026-10-01")); err != nil {
		t.Fatal(err)
	}
	for _, a := range jobs.Args {
		if j, ok := a.(app.InvoiceAccountArgs); ok {
			if err = svc.InvoiceAccount(ctx, j.AccountID, j.Period); err != nil {
				t.Fatal(err)
			}
		}
	}
	sum, err := svc.Summary(ctxB)
	if err != nil || len(sum.OpenInvoices) != 1 {
		t.Fatalf("B's invoice: %+v, %v", sum, err)
	}
	f.invoiceB, f.numberB = sum.OpenInvoices[0].ID.String(), sum.OpenInvoices[0].Number

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

func req(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func (f fixture) do(t *testing.T, r *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func path(s tenant.StoreID, rest string) string {
	return "/v1/stores/" + s.String() + "/billing" + rest
}

func TestGetBilling(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	code, body := f.do(t, req(t, http.MethodGet, path(f.storeB, ""), tokOwnerB))
	invs, _ := body["open_invoices"].([]any)
	if code != http.StatusOK || body["plan_name_en"] != "Pro" || body["status"] != "active" || body["cycle"] != "monthly" || len(invs) != 1 {
		t.Fatalf("B's billing = %d %v", code, body)
	}
	inv := invs[0].(map[string]any)
	if inv["number"] != f.numberB || inv["total_minor"] != float64(59900+8386) || inv["currency"] != "EGP" || inv["period"] != "2026-10-01" {
		t.Fatalf("invoice = %v", inv)
	}
	// A store never invoiced has no invoices; a manager may not look.
	if code, body = f.do(t, req(t, http.MethodGet, path(f.storeA, ""), tokOwnerA)); code != http.StatusOK || len(body["open_invoices"].([]any)) != 0 {
		t.Fatalf("A's billing = %d %v", code, body)
	}
	if code, _ = f.do(t, req(t, http.MethodGet, path(f.storeA, ""), tokManagerA)); code != http.StatusForbidden {
		t.Fatalf("manager = %d", code)
	}
}

func TestPayInvoice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	code, body := f.do(t, req(t, http.MethodPost, path(f.storeB, "/invoices/"+f.invoiceB+"/pay"), tokOwnerB))
	if code != http.StatusOK || !strings.HasPrefix(fmt.Sprint(body["checkout_url"]), "https://pay.example.com/bc_") {
		t.Fatalf("pay = %d %v", code, body)
	}
	for name, p := range map[string]string{"a malformed ID": "/invoices/nope/pay", "an unknown invoice": "/invoices/" + ulid.NewGenerator().New().String() + "/pay"} {
		if code, _ = f.do(t, req(t, http.MethodPost, path(f.storeB, p), tokOwnerB)); code != http.StatusNotFound {
			t.Errorf("%s = %d", name, code)
		}
	}
	if code, _ = f.do(t, req(t, http.MethodPost, path(f.storeA, "/invoices/"+f.invoiceB+"/pay"), tokManagerA)); code != http.StatusForbidden {
		t.Fatalf("manager = %d", code)
	}
}

// TestBillingCrossTenant: store A's owner gets 404 for B's billing and
// B's invoice, and nothing of B leaks.
func TestBillingCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	leaks := []string{f.numberB, f.invoiceB}
	unchanged := func(t testing.TB) {
		t.Helper()
		// No checkout was opened for B's invoice.
		var n int
		ctx := tenant.WithStore(context.Background(), f.storeB)
		if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM billing_checkouts`).Scan(&n)
		}); err != nil || n != 0 {
			t.Fatalf("B's checkouts = %d (%v)", n, err)
		}
	}
	cases := []testkit.CrossTenantCase{
		{OperationID: "getBilling", Request: req(t, http.MethodGet, path(f.storeB, ""), tokOwnerA), Leaks: leaks},
		{OperationID: "payInvoice", Request: req(t, http.MethodPost, path(f.storeB, "/invoices/"+f.invoiceB+"/pay"), tokOwnerA), Leaks: leaks, Unchanged: unchanged},
		// A's store in the path, B's invoice.
		{OperationID: "payInvoice", Request: req(t, http.MethodPost, path(f.storeA, "/invoices/"+f.invoiceB+"/pay"), tokOwnerA), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
}

func mustDate(t *testing.T, v string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
