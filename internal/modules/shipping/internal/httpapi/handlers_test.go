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

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/shiptest"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
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

// TestMain fails the package if any shipping operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "shipping")); len(missing) > 0 {
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
	tokCatalogA  = "catalog-a"
	tokOwnerB    = "owner-b"
	zoneNameOfB  = "منطقة المتجر ب السرية"
	cityNameOfB  = "مدينة ب السرية"
	defaultInput = `{"name":"القاهرة","areas":["EG-C","EG-GZ"],"rate_minor":5000,"cities":[{"name":"المعادي","rate_minor":4000}]}`
)

type fixture struct {
	h              http.Handler
	svc            *app.Service
	storeA, storeB tenant.StoreID
	ctxA, ctxB     context.Context
	zoneB          domain.Zone
	connB          domain.Connection
	shipB          domain.Shipment
	orders         *shiptest.Orders
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
	orders := shiptest.NewOrders()
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Cache: app.DefaultCacheConfig(),
		Carriers: app.Carriers{fake.Name: fake.New()}, Sealer: sealer, PublicURL: "https://app.example.com",
		Orders: orders, Jobs: shiptest.NewJobs(), Objects: shiptest.NewObjects(), Inbox: shiptest.NewInbox(),
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: money.EGP, Country: geo.EG}, nil
		}})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "shh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	_, a := mk()
	ctxB, b := mk()
	connB, err := svc.Connect(ctxB, fake.Name, "b-carrier-key")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := svc.Ship(ctxB, []ulid.ID{orders.Add(t, "ready_to_ship")}, connB.Connection.ID)
	if err != nil || shipped[0].Err != nil {
		t.Fatalf("B's shipment: %+v %v", shipped, err)
	}
	if err = svc.CreateShipment(ctxB, shipped[0].Shipment.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.FetchLabel(ctxB, shipped[0].Shipment.ID); err != nil {
		t.Fatal(err)
	}
	shipB, _, err := svc.Shipment(ctxB, shipped[0].Shipment.ID)
	if err != nil {
		t.Fatal(err)
	}
	zoneB, err := svc.CreateZone(ctxB, domain.ZoneInput{Name: zoneNameOfB, Areas: []string{"EG-ALX"}, RateMinor: 7777,
		Cities: []domain.CityInput{{Name: cityNameOfB, RateMinor: 6666}}})
	if err != nil {
		t.Fatal(err)
	}

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermShippingRead, authz.PermOrdersRead},
		authz.RoleShipping: {authz.PermCatalogRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleOwner}},
		tokSupportA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleSupport}},
		tokCatalogA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{a: authz.RoleShipping}},
		tokOwnerB:   {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{b: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, pol, testkit.FakeStoreScope(resp, members), resp)
	return fixture{h: mux, svc: svc, orders: orders, storeA: a, storeB: b, ctxA: tenant.WithStore(ctx, a), ctxB: ctxB, zoneB: zoneB,
		connB: connB.Connection, shipB: shipB}
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
	return "/v1/stores/" + s.String() + "/shipping" + rest
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, cfg := f.do(t, req(t, http.MethodGet, path(a, ""), tokSupportA, ""))
	if code != http.StatusOK || cfg["currency"] != "EGP" || len(cfg["zones"].([]any)) != 0 || cfg["settings"].(map[string]any)["version"] != float64(0) {
		t.Fatalf("getShipping (empty) = %d %v", code, cfg)
	}
	code, z := f.do(t, req(t, http.MethodPost, path(a, "/zones"), tokOwnerA, defaultInput))
	if code != http.StatusCreated || z["name"] != "القاهرة" || z["version"] != float64(1) || len(z["cities"].([]any)) != 1 {
		t.Fatalf("createShippingZone = %d %v", code, z)
	}
	id := z["id"].(string)
	code, s := f.do(t, req(t, http.MethodPut, path(a, "/settings"), tokOwnerA,
		`{"version":0,"cod_fee":{"fixed_minor":1000,"bp":0,"min_minor":0,"max_minor":0},"free_over_minor":100000}`))
	if code != http.StatusOK || s["version"] != float64(1) || s["free_over_minor"] != float64(100000) {
		t.Fatalf("saveShippingSettings = %d %v", code, s)
	}
	code, q := f.do(t, req(t, http.MethodPost, path(a, "/quote"), tokSupportA, `{"governorate":"EG-C","city":"المعادى","subtotal_minor":50000}`))
	if code != http.StatusOK || q["deliverable"] != true || q["shipping_minor"] != float64(4000) || q["cod_fee_minor"] != float64(1000) ||
		q["zone_id"] != id || q["currency"] != "EGP" {
		t.Fatalf("quoteShipping = %d %v", code, q)
	}
	code, z = f.do(t, req(t, http.MethodPut, path(a, "/zones/"+id), tokOwnerA,
		`{"version":1,"name":"القاهرة","areas":["EG-C"],"rate_minor":4500,"cod_fee":{"fixed_minor":0,"bp":150,"min_minor":500,"max_minor":0}}`))
	if code != http.StatusOK || z["version"] != float64(2) || z["cod_fee"].(map[string]any)["bp"] != float64(150) || len(z["areas"].([]any)) != 1 {
		t.Fatalf("updateShippingZone = %d %v", code, z)
	}
	code, q = f.do(t, req(t, http.MethodPost, path(a, "/quote"), tokOwnerA, `{"governorate":"EG-GZ","subtotal_minor":50000}`))
	if code != http.StatusOK || q["deliverable"] != false || q["zone_id"] != nil {
		t.Fatalf("quote outside zones = %d %v", code, q)
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, path(a, "/zones/"+id), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deleteShippingZone = %d", code)
	}
	if code, cfg = f.do(t, req(t, http.MethodGet, path(a, ""), tokOwnerA, "")); code != http.StatusOK || len(cfg["zones"].([]any)) != 0 {
		t.Fatalf("after delete = %d %v", code, cfg)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, z := f.do(t, req(t, http.MethodPost, path(a, "/zones"), tokOwnerA, defaultInput))
	if code != http.StatusCreated {
		t.Fatal(code, z)
	}
	id := z["id"].(string)
	tests := []struct {
		name, method, path, body string
		want                     int
		field                    string
	}{
		{"governorate taken", http.MethodPost, "/zones", `{"name":"x","areas":["EG-C"],"rate_minor":1}`, http.StatusUnprocessableEntity, "areas"},
		{"foreign governorate", http.MethodPost, "/zones", `{"name":"x","areas":["AE-DU"],"rate_minor":1}`, http.StatusUnprocessableEntity, "areas[0]"},
		{"no areas", http.MethodPost, "/zones", `{"name":"x","areas":[],"rate_minor":1}`, http.StatusUnprocessableEntity, "areas"},
		{"negative rate", http.MethodPost, "/zones", `{"name":"x","areas":["EG-ALX"],"rate_minor":-1}`, http.StatusUnprocessableEntity, "rate_minor"},
		{"duplicate city", http.MethodPost, "/zones", `{"name":"x","areas":["EG-ALX"],"rate_minor":1,"cities":[{"name":"سموحة","rate_minor":1},{"name":"سموحه","rate_minor":2}]}`,
			http.StatusUnprocessableEntity, "cities[1].name"},
		{"no body", http.MethodPost, "/zones", ``, http.StatusBadRequest, ""},
		{"bad json", http.MethodPost, "/zones", `{"name":`, http.StatusBadRequest, ""},
		{"update without version", http.MethodPut, "/zones/" + id, `{"name":"x","areas":["EG-C"],"rate_minor":1}`, http.StatusUnprocessableEntity, "version"},
		{"stale version", http.MethodPut, "/zones/" + id, `{"version":9,"name":"x","areas":["EG-C"],"rate_minor":1}`, http.StatusConflict, ""},
		{"malformed zone id", http.MethodPut, "/zones/nope", `{"version":1,"name":"x","areas":["EG-C"],"rate_minor":1}`, http.StatusNotFound, ""},
		{"unknown zone", http.MethodDelete, "/zones/" + ulid.NewGenerator().New().String(), ``, http.StatusNotFound, ""},
		{"settings fee too high", http.MethodPut, "/settings", `{"version":0,"cod_fee":{"fixed_minor":0,"bp":5000,"min_minor":0,"max_minor":0}}`,
			http.StatusUnprocessableEntity, "cod_fee.bp"},
		{"settings stale", http.MethodPut, "/settings", `{"version":3,"cod_fee":{"fixed_minor":0,"bp":0,"min_minor":0,"max_minor":0}}`, http.StatusConflict, ""},
		{"quote without body", http.MethodPost, "/quote", ``, http.StatusBadRequest, ""},
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

// TestAuthorization: support reads and quotes but changes nothing; a role
// without shipping.read sees nothing; no token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokSupportA, http.MethodPost, "/zones", defaultInput, http.StatusForbidden},
		{tokSupportA, http.MethodPut, "/zones/" + id, defaultInput, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, "/zones/" + id, ``, http.StatusForbidden},
		{tokSupportA, http.MethodPut, "/settings", `{"version":0,"cod_fee":{"fixed_minor":1,"bp":0,"min_minor":0,"max_minor":0}}`, http.StatusForbidden},
		{tokCatalogA, http.MethodGet, "", ``, http.StatusForbidden},
		{tokCatalogA, http.MethodPost, "/quote", `{"governorate":"EG-C","subtotal_minor":1}`, http.StatusForbidden},
		{"", http.MethodGet, "", ``, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		if code, body := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, body, tt.want)
		}
	}
	code, cfg := f.do(t, req(t, http.MethodGet, path(a, ""), tokOwnerA, ""))
	if code != http.StatusOK || len(cfg["zones"].([]any)) != 0 || cfg["settings"].(map[string]any)["version"] != float64(0) {
		t.Fatalf("store A after refused writes = %d %v", code, cfg)
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	zid := f.zoneB.ID.String()
	leaks := []string{zid, zoneNameOfB, cityNameOfB, "7777", "6666"}
	unchanged := func(t testing.TB) {
		t.Helper()
		cfg, err := f.svc.Config(f.ctxB)
		if err != nil || len(cfg.Zones) != 1 || cfg.Zones[0].Name != zoneNameOfB || cfg.Zones[0].RateMinor != 7777 ||
			cfg.Zones[0].Version != 1 || cfg.Settings.Version != 0 {
			t.Fatalf("store B's shipping changed: %+v %v", cfg, err)
		}
	}
	update := `{"version":1,"name":"stolen","areas":["EG-ALX"],"rate_minor":1}`
	sid := f.shipB.ID.String()
	shipUnchanged := func(t testing.TB) {
		t.Helper()
		got, _, err := f.svc.Shipment(f.ctxB, f.shipB.ID)
		if err != nil || got.Status != domain.ShipmentCreated || got.CancelRequestedAt != nil || got.Version != f.shipB.Version {
			t.Fatalf("store B's shipment changed: %+v %v", got, err)
		}
		p, err := f.svc.Shipments(f.ctxB, domain.ShipmentQuery{})
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("store B's shipments: %+v %v", p.Items, err)
		}
	}
	connUnchanged := func(t testing.TB) {
		t.Helper()
		unchanged(t)
		list, err := f.svc.Connections(f.ctxB)
		if err != nil || len(list) != 1 || list[0].Status != "active" {
			t.Fatalf("store B's carrier connections changed: %+v %v", list, err)
		}
	}
	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "getShipping", Request: req(t, http.MethodGet, path(b, ""), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "saveShippingSettings", Request: req(t, http.MethodPut, path(b, "/settings"), tokOwnerA,
			`{"version":0,"cod_fee":{"fixed_minor":1,"bp":0,"min_minor":0,"max_minor":0}}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "createShippingZone", Request: req(t, http.MethodPost, path(b, "/zones"), tokOwnerA, defaultInput), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateShippingZone", Request: req(t, http.MethodPut, path(b, "/zones/"+zid), tokOwnerA, update), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteShippingZone", Request: req(t, http.MethodDelete, path(b, "/zones/"+zid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "quoteShipping", Request: req(t, http.MethodPost, path(b, "/quote"), tokOwnerA, `{"governorate":"EG-ALX","subtotal_minor":1}`),
			Leaks: leaks, Unchanged: unchanged},
		{OperationID: "listCarrierConnections", Request: req(t, http.MethodGet, path(b, "/carriers"), tokOwnerA, ""), Leaks: append(leaks, f.connB.ID.String())},
		{OperationID: "connectCarrier", Request: req(t, http.MethodPost, path(b, "/carriers"), tokOwnerA, `{"carrier":"fake","api_key":"k"}`),
			Leaks: leaks, Unchanged: connUnchanged},
		{OperationID: "disconnectCarrier", Request: req(t, http.MethodDelete, path(b, "/carriers/"+f.connB.ID.String()), tokOwnerA, ""),
			Leaks: leaks, Unchanged: connUnchanged},
		{OperationID: "disconnectCarrier", Request: req(t, http.MethodDelete, path(a, "/carriers/"+f.connB.ID.String()), tokOwnerA, ""),
			Leaks: leaks, Unchanged: connUnchanged},
		{OperationID: "listShipments", Request: req(t, http.MethodGet, path(b, "/shipments"), tokOwnerA, ""), Leaks: append(leaks, sid)},
		{OperationID: "createShipments", Request: req(t, http.MethodPost, path(b, "/shipments"), tokOwnerA,
			`{"connection_id":"`+f.connB.ID.String()+`","order_ids":["`+f.shipB.OrderID.String()+`"]}`), Leaks: leaks, Unchanged: shipUnchanged},
		{OperationID: "getShipment", Request: req(t, http.MethodGet, path(b, "/shipments/"+sid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getShipment", Request: req(t, http.MethodGet, path(a, "/shipments/"+sid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "cancelShipment", Request: req(t, http.MethodPost, path(b, "/shipments/"+sid+"/cancel"), tokOwnerA, ""), Leaks: leaks, Unchanged: shipUnchanged},
		{OperationID: "cancelShipment", Request: req(t, http.MethodPost, path(a, "/shipments/"+sid+"/cancel"), tokOwnerA, ""), Leaks: leaks, Unchanged: shipUnchanged},
		{OperationID: "getShipmentLabel", Request: req(t, http.MethodGet, path(b, "/shipments/"+sid+"/label"), tokOwnerA, ""), Leaks: append(leaks, "%PDF")},
		{OperationID: "getShipmentLabel", Request: req(t, http.MethodGet, path(a, "/shipments/"+sid+"/label"), tokOwnerA, ""), Leaks: append(leaks, "%PDF")},
		// A's store in the path, B's zone ID.
		{OperationID: "updateShippingZone", Request: req(t, http.MethodPut, path(a, "/zones/"+zid), tokOwnerA, update), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteShippingZone", Request: req(t, http.MethodDelete, path(a, "/zones/"+zid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.Method+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// A's own quote for B's governorate knows nothing of B's zone.
	code, q := f.do(t, req(t, http.MethodPost, path(a, "/quote"), tokOwnerA, `{"governorate":"EG-ALX","city":"`+cityNameOfB+`","subtotal_minor":1}`))
	if code != http.StatusOK || q["shipping_minor"] != float64(0) || q["zone_id"] != nil {
		t.Fatalf("A's quote = %d %v", code, q)
	}
}

func TestCarrierConnections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, body := f.do(t, req(t, http.MethodPost, path(a, "/carriers"), tokOwnerA, `{"carrier":"fake","api_key":"a-secret-key"}`))
	conn, _ := body["connection"].(map[string]any)
	if code != http.StatusCreated || conn["carrier"] != "fake" || conn["status"] != "active" || body["webhook_secret"] == "" ||
		!strings.HasPrefix(body["webhook_url"].(string), "https://app.example.com/v1/webhooks/fake/"+a.String()+"/") {
		t.Fatalf("connectCarrier = %d %v", code, body)
	}
	id := conn["id"].(string)
	code, list := f.do(t, req(t, http.MethodGet, path(a, "/carriers"), tokSupportA, ""))
	if code != http.StatusOK || fmt.Sprint(list["carriers"]) != "[fake]" || len(list["connections"].([]any)) != 1 ||
		strings.Contains(fmt.Sprint(list), "a-secret-key") || strings.Contains(fmt.Sprint(list), body["webhook_secret"].(string)) {
		t.Fatalf("listCarrierConnections = %d %v", code, list)
	}
	for _, tt := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"refused key", http.MethodPost, "/carriers", tokOwnerA, `{"carrier":"fake","api_key":"` + fake.InvalidKey + `"}`, http.StatusUnprocessableEntity},
		{"unknown carrier", http.MethodPost, "/carriers", tokOwnerA, `{"carrier":"dhl","api_key":"k"}`, http.StatusUnprocessableEntity},
		{"already connected", http.MethodPost, "/carriers", tokOwnerA, `{"carrier":"fake","api_key":"k2"}`, http.StatusConflict},
		{"no body", http.MethodPost, "/carriers", tokOwnerA, ``, http.StatusBadRequest},
		{"bad id", http.MethodDelete, "/carriers/nope", tokOwnerA, ``, http.StatusNotFound},
		{"support cannot connect", http.MethodPost, "/carriers", tokSupportA, `{"carrier":"fake","api_key":"k"}`, http.StatusForbidden},
		{"support cannot disconnect", http.MethodDelete, "/carriers/" + id, tokSupportA, ``, http.StatusForbidden},
		{"no shipping.read", http.MethodGet, "/carriers", tokCatalogA, ``, http.StatusForbidden},
	} {
		if got, out := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); got != tt.want {
			t.Errorf("%s: %d %v, want %d", tt.name, got, out, tt.want)
		}
	}
	if code, _ = f.do(t, req(t, http.MethodDelete, path(a, "/carriers/"+id), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("disconnectCarrier = %d", code)
	}
	code, list = f.do(t, req(t, http.MethodGet, path(a, "/carriers"), tokOwnerA, ""))
	c0 := list["connections"].([]any)[0].(map[string]any)
	if code != http.StatusOK || c0["status"] != "disconnected" || c0["webhook_url"] != nil || c0["disconnected_at"] == nil {
		t.Fatalf("after disconnect = %d %v", code, list)
	}
}

func TestShipmentsAPI(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, body := f.do(t, req(t, http.MethodPost, path(a, "/carriers"), tokOwnerA, `{"carrier":"fake","api_key":"a-key"}`))
	if code != http.StatusCreated {
		t.Fatal(code, body)
	}
	conn := body["connection"].(map[string]any)["id"].(string)
	ready, pending := f.orders.Add(t, "ready_to_ship"), f.orders.Add(t, "pending")
	code, body = f.do(t, req(t, http.MethodPost, path(a, "/shipments"), tokOwnerA,
		`{"connection_id":"`+conn+`","order_ids":["`+ready.String()+`","`+pending.String()+`","nope"]}`))
	results, _ := body["results"].([]any)
	if code != http.StatusOK || len(results) != 3 {
		t.Fatalf("createShipments = %d %v", code, body)
	}
	first := results[0].(map[string]any)
	sh := first["shipment"].(map[string]any)
	if sh["status"] != "requested" || sh["cod_minor"] != float64(52000) || sh["label_ready"] != false {
		t.Fatalf("shipment %v", sh)
	}
	for i, wantCode := range []string{"order_not_ready", "order_not_found"} {
		if e := results[i+1].(map[string]any)["error"].(map[string]any); e["code"] != wantCode {
			t.Errorf("result %d: %v, want %s", i+1, e, wantCode)
		}
	}
	id := sh["id"].(string)
	sid, err := ulid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	if code, body = f.do(t, req(t, http.MethodGet, path(a, "/shipments/"+id+"/label"), tokOwnerA, "")); code != http.StatusConflict || body["code"] != "label_not_ready" {
		t.Fatalf("label before the carrier = %d %v", code, body)
	}
	if err = f.svc.CreateShipment(f.ctxA, sid); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.FetchLabel(f.ctxA, sid); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req(t, http.MethodGet, path(a, "/shipments/"+id+"/label"), tokSupportA, ""))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Fatalf("label = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	code, detail := f.do(t, req(t, http.MethodGet, path(a, "/shipments/"+id), tokSupportA, ""))
	got := detail["shipment"].(map[string]any)
	if code != http.StatusOK || got["status"] != "created" || got["tracking_number"] == nil || got["label_ready"] != true || len(detail["events"].([]any)) != 2 {
		t.Fatalf("getShipment = %d %v", code, detail)
	}
	code, page := f.do(t, req(t, http.MethodGet, path(a, "/shipments?order_id="+ready.String()+"&status=created&limit=1"), tokSupportA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("listShipments = %d %v", code, page)
	}
	code, cancelled := f.do(t, req(t, http.MethodPost, path(a, "/shipments/"+id+"/cancel"), tokOwnerA, ""))
	if code != http.StatusOK || cancelled["cancel_requested_at"] == nil {
		t.Fatalf("cancelShipment = %d %v", code, cancelled)
	}

	for _, tt := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"no orders", http.MethodPost, "/shipments", tokOwnerA, `{"connection_id":"` + conn + `","order_ids":[]}`, http.StatusUnprocessableEntity},
		{"unknown connection", http.MethodPost, "/shipments", tokOwnerA, `{"connection_id":"` + f.connB.ID.String() + `","order_ids":["` + ready.String() + `"]}`,
			http.StatusUnprocessableEntity},
		{"bad connection id", http.MethodPost, "/shipments", tokOwnerA, `{"connection_id":"x","order_ids":["` + ready.String() + `"]}`, http.StatusUnprocessableEntity},
		{"bad cursor", http.MethodGet, "/shipments?cursor=zzz", tokOwnerA, ``, http.StatusUnprocessableEntity},
		{"bad shipment id", http.MethodGet, "/shipments/nope", tokOwnerA, ``, http.StatusNotFound},
		{"unknown shipment", http.MethodPost, "/shipments/" + ulid.NewGenerator().New().String() + "/cancel", tokOwnerA, ``, http.StatusNotFound},
		{"support cannot ship", http.MethodPost, "/shipments", tokSupportA, `{"connection_id":"` + conn + `","order_ids":["` + ready.String() + `"]}`, http.StatusForbidden},
		{"support cannot cancel", http.MethodPost, "/shipments/" + id + "/cancel", tokSupportA, ``, http.StatusForbidden},
		{"no orders.read", http.MethodGet, "/shipments", tokCatalogA, ``, http.StatusForbidden},
		{"no orders.read for labels", http.MethodGet, "/shipments/" + id + "/label", tokCatalogA, ``, http.StatusForbidden},
	} {
		if got, out := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); got != tt.want {
			t.Errorf("%s: %d %v, want %d", tt.name, got, out, tt.want)
		}
	}
}
