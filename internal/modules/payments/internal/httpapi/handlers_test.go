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

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/paytest"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
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

// TestMain fails the package if any payments operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "payments")); len(missing) > 0 {
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
	tokManagerA  = "manager-a"
	tokSupportA  = "support-a"
	tokCatalogA  = "catalog-a"
	tokOwnerB    = "owner-b"
	apiKey       = "merchant-api-key"
	hookSecret   = "merchant-hook-secret"
	reasonOfB    = "سبب استرداد المتجر ب السري"
	connectInput = `{"gateway":"fakepay","api_key":"` + apiKey + `","webhook_secret":"` + hookSecret + `"}`
)

type fixture struct {
	h              http.Handler
	svc            *app.Service
	gw             *fake.Gateway
	orders         *paytest.Orders
	storeA, storeB tenant.StoreID
	ctxA, ctxB     context.Context
	connB          domain.Connection
	payB           domain.Payment
	orderB         ulid.ID
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
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{gw: fake.New(), orders: paytest.NewOrders()}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: ids, Gateways: app.Gateways{fake.Name: f.gw}, Sealer: sealer,
		PublicURL: "https://app.example.com", Orders: f.orders, Jobs: testkit.NewJobs(), Inbox: webhookstest.NewInbox()})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "pyh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	f.ctxA, f.storeA = mk()
	f.ctxB, f.storeB = mk()
	if f.connB, err = f.svc.Connect(f.ctxB, app.ConnectInput{Gateway: fake.Name, APIKey: apiKey, WebhookSecret: hookSecret}); err != nil {
		t.Fatal(err)
	}
	f.orderB = f.orders.Add(t, orders.PaymentCard, 0)
	f.payB = f.paid(f.ctxB, t, f.orderB)
	if _, err = f.svc.Refund(f.ctxB, f.payB.ID, 1000, reasonOfB); err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleManager:  {authz.PermOrdersRead, authz.PermPaymentsRefund},
		authz.RoleSupport:  {authz.PermOrdersRead},
		authz.RoleShipping: {authz.PermCatalogRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokManagerA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleManager}},
		tokSupportA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokCatalogA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleShipping}},
		tokOwnerB:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, f.svc, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = mux
	return f
}

// paid starts a card order's checkout in ctx's store and has the shopper
// pay it.
func (f fixture) paid(ctx context.Context, t *testing.T, order ulid.ID) domain.Payment {
	t.Helper()
	c, err := f.svc.StartCheckout(ctx, order, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.gw.Pay(apiKey, c.PaymentID.String()); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Refresh(ctx, c.PaymentID)
	if err != nil || p.Status != domain.StatusPaid {
		t.Fatalf("paid: %+v %v", p, err)
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
	return "/v1/stores/" + s.String() + "/payments" + rest
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, list := f.do(t, req(t, http.MethodGet, path(a, "/gateways"), tokOwnerA, ""))
	if code != http.StatusOK || fmt.Sprint(list["gateways"]) != "[fakepay]" || len(list["connections"].([]any)) != 0 {
		t.Fatalf("listPaymentConnections = %d %v", code, list)
	}
	code, conn := f.do(t, req(t, http.MethodPost, path(a, "/gateways"), tokOwnerA, connectInput))
	if code != http.StatusCreated || conn["status"] != "active" || conn["gateway"] != "fakepay" ||
		conn["webhook_url"] != "https://app.example.com/v1/webhooks/fakepay/"+a.String()+"/"+conn["id"].(string) {
		t.Fatalf("connectPaymentGateway = %d %v", code, conn)
	}
	if _, list = f.do(t, req(t, http.MethodGet, path(a, "/gateways"), tokOwnerA, "")); len(list["connections"].([]any)) != 1 {
		t.Fatalf("connections after connect: %v", list)
	}

	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.paid(f.ctxA, t, order)
	code, page := f.do(t, req(t, http.MethodGet, path(a, ""), tokSupportA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("listPayments = %d %v", code, page)
	}
	item := page["items"].([]any)[0].(map[string]any)
	if item["id"] != p.ID.String() || item["status"] != "paid" || item["amount_minor"] != float64(52000) || item["currency"] != "EGP" ||
		item["transaction_id"] == nil || item["order_id"] != order.String() {
		t.Fatalf("payment %v", item)
	}
	if _, page = f.do(t, req(t, http.MethodGet, path(a, "?order_id="+order.String()), tokSupportA, "")); len(page["items"].([]any)) != 1 {
		t.Fatalf("listPayments by order: %v", page)
	}

	code, r := f.do(t, req(t, http.MethodPost, path(a, "/"+p.ID.String()+"/refunds"), tokManagerA, `{"amount_minor":20000,"reason":"مرتجع جزئي"}`))
	if code != http.StatusAccepted || r["status"] != "requested" || r["amount_minor"] != float64(20000) || r["reason"] != "مرتجع جزئي" {
		t.Fatalf("refundPayment = %d %v", code, r)
	}
	code, detail := f.do(t, req(t, http.MethodGet, path(a, "/"+p.ID.String()), tokSupportA, ""))
	if code != http.StatusOK || detail["payment"].(map[string]any)["id"] != p.ID.String() || len(detail["refunds"].([]any)) != 1 {
		t.Fatalf("getPayment = %d %v", code, detail)
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, path(a, "/gateways/"+conn["id"].(string)), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("disconnectPaymentGateway = %d", code)
	}
}

func TestPaging(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	if code, _ := f.do(t, req(t, http.MethodPost, path(a, "/gateways"), tokOwnerA, connectInput)); code != http.StatusCreated {
		t.Fatal(code)
	}
	for range 3 {
		if _, err := f.svc.StartCheckout(f.ctxA, f.orders.Add(t, orders.PaymentCard, 0), ""); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	cursor := ""
	for range 3 {
		code, page := f.do(t, req(t, http.MethodGet, path(a, "?limit=2"+cursor), tokOwnerA, ""))
		if code != http.StatusOK {
			t.Fatalf("page = %d %v", code, page)
		}
		seen += len(page["items"].([]any))
		next, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = "&cursor=" + next
	}
	if seen != 3 {
		t.Fatalf("paged through %d payments, want 3", seen)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	if code, _ := f.do(t, req(t, http.MethodPost, path(a, "/gateways"), tokOwnerA, connectInput)); code != http.StatusCreated {
		t.Fatal(code)
	}
	p := f.paid(f.ctxA, t, f.orders.Add(t, orders.PaymentCard, 0))
	c, err := f.svc.StartCheckout(f.ctxA, f.orders.Add(t, orders.PaymentCard, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	refunds := "/" + p.ID.String() + "/refunds"
	tests := []struct {
		name, method, path, body string
		want                     int
		field                    string
	}{
		{"second gateway", http.MethodPost, "/gateways", connectInput, http.StatusConflict, ""},
		{"unknown gateway", http.MethodPost, "/gateways", `{"gateway":"stripe","api_key":"k","webhook_secret":"s"}`, http.StatusUnprocessableEntity, "gateway"},
		{"no body", http.MethodPost, "/gateways", ``, http.StatusBadRequest, ""},
		{"bad json", http.MethodPost, "/gateways", `{"gateway":`, http.StatusBadRequest, ""},
		{"zero refund", http.MethodPost, refunds, `{"amount_minor":0}`, http.StatusUnprocessableEntity, "amount_minor"},
		{"refund too much", http.MethodPost, refunds, `{"amount_minor":52001}`, http.StatusUnprocessableEntity, "amount_minor"},
		{"long reason", http.MethodPost, refunds, `{"amount_minor":1,"reason":"` + strings.Repeat("x", 301) + `"}`, http.StatusUnprocessableEntity, "reason"},
		{"refund without body", http.MethodPost, refunds, ``, http.StatusBadRequest, ""},
		{"refund unpaid", http.MethodPost, "/" + c.PaymentID.String() + "/refunds", `{"amount_minor":1}`, http.StatusConflict, ""},
		{"malformed payment", http.MethodGet, "/nope", ``, http.StatusNotFound, ""},
		{"unknown payment", http.MethodGet, "/" + ulid.NewGenerator().New().String(), ``, http.StatusNotFound, ""},
		{"malformed refund target", http.MethodPost, "/nope/refunds", `{"amount_minor":1}`, http.StatusNotFound, ""},
		{"malformed connection", http.MethodDelete, "/gateways/nope", ``, http.StatusNotFound, ""},
		{"bad cursor", http.MethodGet, "?cursor=nope", ``, http.StatusUnprocessableEntity, "cursor"},
		{"bad cursor id", http.MethodGet, "?cursor=1.nope", ``, http.StatusUnprocessableEntity, "cursor"},
		{"limit too big", http.MethodGet, "?limit=500", ``, http.StatusUnprocessableEntity, "limit"},
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
	// A malformed order filter matches nothing.
	if code, page := f.do(t, req(t, http.MethodGet, path(a, "?order_id=nope"), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("malformed order filter = %d %v", code, page)
	}
}

// TestAuthorization: only the owner connects a gateway; the manager
// refunds; support reads; a role without orders.read sees nothing; no
// token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokManagerA, http.MethodGet, "/gateways", ``, http.StatusForbidden},
		{tokManagerA, http.MethodPost, "/gateways", connectInput, http.StatusForbidden},
		{tokManagerA, http.MethodDelete, "/gateways/" + id, ``, http.StatusForbidden},
		{tokSupportA, http.MethodPost, "/" + id + "/refunds", `{"amount_minor":1}`, http.StatusForbidden},
		{tokSupportA, http.MethodGet, "/gateways", ``, http.StatusForbidden},
		{tokCatalogA, http.MethodGet, "", ``, http.StatusForbidden},
		{tokCatalogA, http.MethodGet, "/" + id, ``, http.StatusForbidden},
		{"", http.MethodGet, "", ``, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		if code, body := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, body, tt.want)
		}
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	pid, cid := f.payB.ID.String(), f.connB.ID.String()
	leaks := []string{pid, cid, f.orderB.String(), reasonOfB, f.payB.TransactionID}
	unchanged := func(t testing.TB) {
		t.Helper()
		p, refunds, err := f.svc.Payment(f.ctxB, f.payB.ID)
		if err != nil || p.Status != domain.StatusPaid || len(refunds) != 1 || refunds[0].Amount.Amount() != 1000 {
			t.Fatalf("store B's payment changed: %+v %+v %v", p, refunds, err)
		}
		list, err := f.svc.Connections(f.ctxB)
		if err != nil || len(list) != 1 || list[0].Status != domain.ConnectionActive {
			t.Fatalf("store B's gateway changed: %+v %v", list, err)
		}
	}
	refund := `{"amount_minor":100}`
	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "listPaymentConnections", Request: req(t, http.MethodGet, path(b, "/gateways"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "connectPaymentGateway", Request: req(t, http.MethodPost, path(b, "/gateways"), tokOwnerA, connectInput), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "disconnectPaymentGateway", Request: req(t, http.MethodDelete, path(b, "/gateways/"+cid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listPayments", Request: req(t, http.MethodGet, path(b, ""), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getPayment", Request: req(t, http.MethodGet, path(b, "/"+pid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "refundPayment", Request: req(t, http.MethodPost, path(b, "/"+pid+"/refunds"), tokOwnerA, refund), Leaks: leaks, Unchanged: unchanged},
		// A's store in the path, B's IDs.
		{OperationID: "disconnectPaymentGateway", Request: req(t, http.MethodDelete, path(a, "/gateways/"+cid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "getPayment", Request: req(t, http.MethodGet, path(a, "/"+pid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "refundPayment", Request: req(t, http.MethodPost, path(a, "/"+pid+"/refunds"), tokOwnerA, refund), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.Method+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// A's own list, filtered by B's order, is empty.
	code, page := f.do(t, req(t, http.MethodGet, path(a, "?order_id="+f.orderB.String()), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("A's payments of B's order = %d %v", code, page)
	}
	// B's owner sees their own.
	if code, page = f.do(t, req(t, http.MethodGet, path(b, ""), tokOwnerB, "")); code != http.StatusOK || len(page["items"].([]any)) != 1 {
		t.Fatalf("B's payments = %d %v", code, page)
	}
}
