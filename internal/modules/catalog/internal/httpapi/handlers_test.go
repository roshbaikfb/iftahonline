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
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/search"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
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

// TestMain fails the package if any catalog operation under
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
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "catalog")); len(missing) > 0 {
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

// testPolicy mirrors identity's matrix for the catalog permissions; it
// exercises the handlers' permission checks, not the matrix itself.
func testPolicy(t *testing.T) authz.Policy {
	t.Helper()
	p, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermCatalogRead},
		authz.RoleShipping: {authz.PermCatalogRead, authz.PermInventoryManage},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type currency money.Currency

func (c currency) StoreCurrency(context.Context) (money.Currency, error) {
	return money.Currency(c), nil
}

const (
	tokOwnerA    = "owner-a"
	tokSupportA  = "support-a"
	tokShippingA = "shipping-a"
	tokOwnerB    = "owner-b"
)

type fixture struct {
	h        http.Handler
	svc      *app.Service
	storeA   tenant.StoreID
	storeB   tenant.StoreID
	ctxB     context.Context
	productB domain.Product
	catB     domain.Category
	imageB   domain.Image
	importB  domain.Import
	chartB   domain.SizeChart
}

func testImports(t *testing.T) app.Imports {
	t.Helper()
	kr, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	return app.Imports{Keyring: kr, HTTP: safehttp.New(safehttp.Options{})}
}

// storeIndex answers every search with one hit of the store in ctx, so
// tests see the tenant the handler passed down.
type storeIndex struct{}

func (storeIndex) Upsert(context.Context, []search.Doc) error { return nil }
func (storeIndex) Delete(context.Context, []ulid.ID) error    { return nil }
func (storeIndex) Search(ctx context.Context, q search.Query) (search.Result, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return search.Result{}, err
	}
	return search.Result{EstimatedTotal: 1, Hits: []search.Doc{{
		ID: id.String(), StoreID: id.String(), Title: "hit for " + q.Text, Slug: "hit", Status: "active",
		PriceMinor: 100, Currency: "EGP", ImageKey: "stores/x/images/y.jpg",
	}}}, nil
}

// presignOnly is a bucket that only presigns (handlers never read files).
type presignOnly struct{}

func (presignOnly) PresignUpload(_ context.Context, key, ct string, maxBytes int64, ttl time.Duration) (media.Upload, error) {
	return media.Upload{URL: "https://s3.test/bucket", Fields: map[string]string{"key": key, "Content-Type": ct},
		ExpiresAt: time.Now().Add(ttl), MaxBytes: maxBytes}, nil
}
func (presignOnly) Get(context.Context, string, int64) ([]byte, error) { return nil, media.ErrNoObject }
func (presignOnly) Put(context.Context, string, string, []byte) error  { return nil }
func (presignOnly) Delete(context.Context, string) error               { return nil }

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	svc := app.NewService(app.Deps{DB: pool, Jobs: ins, IDs: ids, Audit: audit.Nop{}, Currency: currency(money.EGP),
		Media:  app.Media{Objects: presignOnly{}, Limits: media.Limits{MaxBytes: 1 << 20, MaxPixels: 1 << 20}},
		Search: storeIndex{}, Imports: testImports(t)})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "http-"+strings.ToLower(id.String()[14:]))
			return execErr
		})
		if txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	_, a := mk()
	ctxB, b := mk()
	catB, err := svc.CreateCategory(ctxB, domain.CategoryInput{Name: "B secret category"})
	if err != nil {
		t.Fatal(err)
	}
	productB, err := svc.CreateProduct(ctxB, domain.ProductInput{Title: "B secret product", Status: "active", CategoryIDs: []ulid.ID{catB.ID},
		Variants: []domain.VariantInput{{SKU: "B-SKU-1", PriceMinor: 999, OnHand: 7}}})
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
	signer := media.NewSigner("https://img.test", "bucket", []byte("0123456789abcdef"), []byte("salt-0123456789a"))
	httpapi.Mount(mux, svc, signer, testPolicy(t), testkit.FakeStoreScope(resp, members), resp)
	imageB, _, err := svc.CreateImageUpload(ctxB, productB.ID, "image/jpeg", domain.ImageInput{Alt: "B secret alt"})
	if err != nil {
		t.Fatal(err)
	}
	importB, _, err := svc.CreateImport(ctxB, app.NewImportInput{Kind: "woocommerce", WooURL: "https://b-secret-shop.example.com",
		WooKey: "ck_bsecret", WooSecret: "cs_bsecret"})
	if err != nil {
		t.Fatal(err)
	}
	chartB, err := svc.CreateSizeChart(ctxB, domain.SizeChartInput{Name: "B secret chart", Header: []string{"المقاس", "الصدر"},
		Body: [][]string{{"S", "96"}}})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{h: mux, svc: svc, storeA: a, storeB: b, ctxB: ctxB, productB: productB, catB: catB, imageB: imageB, importB: importB,
		chartB: chartB}
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

const teeBody = `{"title":"تيشيرت قطن","status":"active",
  "options":[{"name":"المقاس","values":["S","M"]}],
  "variants":[{"sku":"TEE-S","option_values":["S"],"price_minor":15000,"compare_at_minor":20000,"on_hand":4},
              {"sku":"TEE-M","option_values":["M"],"price_minor":16000,"tracked":false}]}`

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA

	code, cat := f.do(t, req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"ملابس"}`))
	if code != http.StatusCreated || cat["slug"] != "ملابس" {
		t.Fatalf("createCategory = %d %v", code, cat)
	}
	catID := cat["id"].(string)
	code, child := f.do(t, req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"Men","parent_id":"`+catID+`","position":2}`))
	if code != http.StatusCreated || child["parent_id"] != catID || child["position"] != float64(2) {
		t.Fatalf("createCategory child = %d %v", code, child)
	}
	childID := child["id"].(string)
	if c, body := f.do(t, req(t, http.MethodPut, storePath(a, "/categories/"+childID), tokOwnerA, `{"name":"Men","slug":"men-wear"}`)); c != http.StatusOK || body["slug"] != "men-wear" || body["parent_id"] != nil {
		t.Fatalf("updateCategory = %d %v", c, body)
	}
	if c, body := f.do(t, req(t, http.MethodGet, storePath(a, "/categories"), tokOwnerA, "")); c != http.StatusOK || len(body["items"].([]any)) != 2 {
		t.Fatalf("listCategories = %d %v", c, body)
	}

	body := strings.Replace(teeBody, `"status":"active",`, `"status":"active","category_ids":["`+catID+`"],`, 1)
	code, p := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, body))
	if code != http.StatusCreated || p["slug"] != "تيشيرت-قطن" || len(p["category_ids"].([]any)) != 1 {
		t.Fatalf("createProduct = %d %v", code, p)
	}
	vs := p["variants"].([]any)
	v0, v1 := vs[0].(map[string]any), vs[1].(map[string]any)
	if v0["currency"] != "EGP" || v0["price_minor"] != float64(15000) || v0["compare_at_minor"] != float64(20000) || v0["sku"] != "TEE-S" {
		t.Fatalf("variant = %v", v0)
	}
	if st := v0["stock"].(map[string]any); st["on_hand"] != float64(4) || st["available"] != float64(4) || st["tracked"] != true {
		t.Fatalf("stock = %v", st)
	}
	if st := v1["stock"].(map[string]any); st["tracked"] != false {
		t.Fatalf("untracked stock = %v", st)
	}
	pid, vid := p["id"].(string), v0["id"].(string)

	if c, got := f.do(t, req(t, http.MethodGet, storePath(a, "/products/"+pid), tokOwnerA, "")); c != http.StatusOK || got["title"] != "تيشيرت قطن" {
		t.Fatalf("getProduct = %d %v", c, got)
	}
	upd := `{"title":"تيشيرت","slug":"tee","variants":[{"id":"` + vid + `","sku":"TEE-S","price_minor":14000}]}`
	code, got := f.do(t, req(t, http.MethodPut, storePath(a, "/products/"+pid), tokOwnerA, upd))
	if code != http.StatusOK || got["slug"] != "tee" || got["status"] != "draft" || len(got["variants"].([]any)) != 1 || len(got["options"].([]any)) != 0 {
		t.Fatalf("updateProduct = %d %v", code, got)
	}
	if v := got["variants"].([]any)[0].(map[string]any); v["id"] != vid || v["stock"].(map[string]any)["on_hand"] != float64(4) {
		t.Fatalf("kept variant = %v", v)
	}
	if c, st := f.do(t, req(t, http.MethodPut, storePath(a, "/variants/"+vid+"/inventory"), tokOwnerA, `{"on_hand":9}`)); c != http.StatusOK || st["on_hand"] != float64(9) || st["tracked"] != true {
		t.Fatalf("setInventory = %d %v", c, st)
	}

	code, iu := f.do(t, req(t, http.MethodPost, storePath(a, "/products/"+pid+"/images"), tokOwnerA, `{"content_type":"image/webp","alt":"أمامية"}`))
	if code != http.StatusCreated {
		t.Fatalf("createImageUpload = %d %v", code, iu)
	}
	image, upload := iu["image"].(map[string]any), iu["upload"].(map[string]any)
	if image["status"] != "pending" || image["urls"] != nil || upload["max_bytes"] != float64(1<<20) ||
		upload["fields"].(map[string]any)["Content-Type"] != "image/webp" {
		t.Fatalf("upload = %v", iu)
	}
	iid := image["id"].(string)
	if c, body := f.do(t, req(t, http.MethodPost, storePath(a, "/products/"+pid+"/images/"+iid+"/complete"), tokOwnerA, "")); c != http.StatusAccepted || body["status"] != "processing" {
		t.Fatalf("completeImageUpload = %d %v", c, body)
	}
	if c, body := f.do(t, req(t, http.MethodPatch, storePath(a, "/products/"+pid+"/images/"+iid), tokOwnerA, `{"alt":"front","position":2}`)); c != http.StatusOK || body["alt"] != "front" || body["position"] != float64(2) {
		t.Fatalf("updateImage = %d %v", c, body)
	}
	if c, got := f.do(t, req(t, http.MethodGet, storePath(a, "/products/"+pid), tokOwnerA, "")); c != http.StatusOK || len(got["images"].([]any)) != 1 {
		t.Fatalf("product images = %d %v", c, got["images"])
	}
	if c, _ := f.do(t, req(t, http.MethodDelete, storePath(a, "/products/"+pid+"/images/"+iid), tokOwnerA, "")); c != http.StatusNoContent {
		t.Fatalf("deleteImage = %d", c)
	}

	code, sr := f.do(t, req(t, http.MethodGet, storePath(a, "/products/search?q=%D8%A3%D8%AD%D8%B0%D9%8A%D8%A9&status=active&limit=5"), tokSupportA, ""))
	if code != http.StatusOK || sr["estimated_total"] != float64(1) {
		t.Fatalf("searchProducts = %d %v", code, sr)
	}
	hit := sr["items"].([]any)[0].(map[string]any)
	if hit["id"] != a.String() || hit["title"] != "hit for احذيه" || hit["thumb"].(map[string]any)["webp"] == "" || hit["category_ids"] == nil {
		t.Fatalf("hit = %v (the store and normalized text must come from the handler)", hit)
	}

	code, ic := f.do(t, req(t, http.MethodPost, storePath(a, "/imports"), tokOwnerA, `{"kind":"csv"}`))
	if code != http.StatusCreated || ic["import"].(map[string]any)["status"] != "pending" || ic["upload"].(map[string]any)["fields"].(map[string]any)["Content-Type"] != "text/csv" {
		t.Fatalf("createImport = %d %v", code, ic)
	}
	impID := ic["import"].(map[string]any)["id"].(string)
	code, wc := f.do(t, req(t, http.MethodPost, storePath(a, "/imports"), tokOwnerA,
		`{"kind":"woocommerce","woocommerce":{"url":"https://shop.example.com","consumer_key":"ck_abc","consumer_secret":"cs_topsecret"}}`))
	if code != http.StatusCreated || wc["upload"] != nil || wc["import"].(map[string]any)["woocommerce_url"] != "https://shop.example.com" {
		t.Fatalf("createImport woocommerce = %d %v", code, wc)
	}
	if raw, _ := json.Marshal(wc); strings.Contains(string(raw), "cs_topsecret") || strings.Contains(string(raw), "ck_abc") {
		t.Fatal("createImport echoes the WooCommerce keys")
	}
	if c, body := f.do(t, req(t, http.MethodPost, storePath(a, "/imports/"+impID+"/start"), tokOwnerA, "")); c != http.StatusAccepted || body["status"] != "queued" {
		t.Fatalf("startImport = %d %v", c, body)
	}
	if c, body := f.do(t, req(t, http.MethodGet, storePath(a, "/imports/"+impID), tokOwnerA, "")); c != http.StatusOK || body["processed"] != float64(0) || body["row_errors"] == nil {
		t.Fatalf("getImport = %d %v", c, body)
	}
	if c, body := f.do(t, req(t, http.MethodGet, storePath(a, "/imports"), tokOwnerA, "")); c != http.StatusOK || len(body["items"].([]any)) != 2 {
		t.Fatalf("listImports = %d %v", c, body)
	}

	mustCreate := func(title string) {
		if c, b := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"`+title+`","variants":[{"price_minor":1}]}`)); c != http.StatusCreated {
			t.Fatalf("create %s = %d %v", title, code, b)
		}
	}
	mustCreate("two")
	mustCreate("three")
	code, page := f.do(t, req(t, http.MethodGet, storePath(a, "/products?limit=2&status=draft"), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 2 || page["next_cursor"] == nil {
		t.Fatalf("listProducts = %d %v", code, page)
	}
	code, page = f.do(t, req(t, http.MethodGet, storePath(a, "/products?limit=2&status=draft&cursor="+page["next_cursor"].(string)), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("listProducts page 2 = %d %v", code, page)
	}
	if code, page = f.do(t, req(t, http.MethodGet, storePath(a, "/products?category_id="+catID), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("listProducts by category = %d %v", code, page)
	}

	if c, _ := f.do(t, req(t, http.MethodDelete, storePath(a, "/products/"+pid), tokOwnerA, "")); c != http.StatusNoContent {
		t.Fatalf("deleteProduct = %d", c)
	}
	if c, _ := f.do(t, req(t, http.MethodGet, storePath(a, "/products/"+pid), tokOwnerA, "")); c != http.StatusNotFound {
		t.Fatalf("deleted product = %d", c)
	}
	if c, _ := f.do(t, req(t, http.MethodDelete, storePath(a, "/categories/"+childID), tokOwnerA, "")); c != http.StatusNoContent {
		t.Fatalf("deleteCategory = %d", c)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	tests := []struct {
		name   string
		rq     *http.Request
		status int
		code   string
		field  string
	}{
		{"empty title", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"","variants":[{"price_minor":1}]}`), 422, "validation_failed", "title"},
		{"no variants", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","variants":[]}`), 422, "validation_failed", "variants"},
		{"negative price", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","variants":[{"price_minor":-5}]}`), 422, "validation_failed", "variants[0].price_minor"},
		{"unknown option value", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","options":[{"name":"Size","values":["S"]}],"variants":[{"option_values":["XL"],"price_minor":1}]}`), 422, "validation_failed", "variants[0].option_values[0]"},
		{"bad status", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","status":"live","variants":[{"price_minor":1}]}`), 422, "validation_failed", "status"},
		{"malformed category id", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","category_ids":["nope"],"variants":[{"price_minor":1}]}`), 422, "validation_failed", "category_ids[0]"},
		{"unknown category", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","category_ids":["`+id+`"],"variants":[{"price_minor":1}]}`), 422, "validation_failed", "category_ids"},
		{"malformed variant id", req(t, http.MethodPut, storePath(a, "/products/"+id), tokOwnerA, `{"title":"x","variants":[{"id":"bad","price_minor":1}]}`), 422, "validation_failed", "variants[0].id"},
		{"malformed json", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":`), 400, "", ""},
		{"bad list status", req(t, http.MethodGet, storePath(a, "/products?status=gone"), tokOwnerA, ""), 422, "validation_failed", "status"},
		{"bad cursor", req(t, http.MethodGet, storePath(a, "/products?cursor=nope"), tokOwnerA, ""), 422, "validation_failed", "cursor"},
		{"bad category filter", req(t, http.MethodGet, storePath(a, "/products?category_id=nope"), tokOwnerA, ""), 422, "validation_failed", "category_id"},
		{"bad product id", req(t, http.MethodGet, storePath(a, "/products/nope"), tokOwnerA, ""), 404, "product_not_found", ""},
		{"missing product", req(t, http.MethodPut, storePath(a, "/products/"+id), tokOwnerA, `{"title":"x","variants":[{"price_minor":1}]}`), 404, "product_not_found", ""},
		{"missing product delete", req(t, http.MethodDelete, storePath(a, "/products/"+id), tokOwnerA, ""), 404, "product_not_found", ""},
		{"negative stock", req(t, http.MethodPut, storePath(a, "/variants/"+id+"/inventory"), tokOwnerA, `{"on_hand":-1}`), 422, "validation_failed", "on_hand"},
		{"bad variant id", req(t, http.MethodPut, storePath(a, "/variants/nope/inventory"), tokOwnerA, `{"on_hand":1}`), 404, "variant_not_found", ""},
		{"empty category name", req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":""}`), 422, "validation_failed", "name"},
		{"malformed parent", req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"x","parent_id":"nope"}`), 422, "validation_failed", "parent_id"},
		{"unknown parent", req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"x","parent_id":"`+id+`"}`), 422, "validation_failed", "parent_id"},
		{"bad category id", req(t, http.MethodPut, storePath(a, "/categories/nope"), tokOwnerA, `{"name":"x"}`), 404, "category_not_found", ""},
		{"missing category delete", req(t, http.MethodDelete, storePath(a, "/categories/"+id), tokOwnerA, ""), 404, "category_not_found", ""},
		{"long search", req(t, http.MethodGet, storePath(a, "/products/search?q="+strings.Repeat("a", 201)), tokOwnerA, ""), 422, "validation_failed", "q"},
		{"search offset", req(t, http.MethodGet, storePath(a, "/products/search?offset=5000"), tokOwnerA, ""), 422, "validation_failed", "offset"},
		{"search category", req(t, http.MethodGet, storePath(a, "/products/search?category_id=nope"), tokOwnerA, ""), 422, "validation_failed", "category_id"},
		{"import kind", req(t, http.MethodPost, storePath(a, "/imports"), tokOwnerA, `{"kind":"pdf"}`), 422, "validation_failed", "kind"},
		{"woo without https", req(t, http.MethodPost, storePath(a, "/imports"), tokOwnerA, `{"kind":"woocommerce","woocommerce":{"url":"http://x.example.com","consumer_key":"ck_1","consumer_secret":"cs_1"}}`), 422, "validation_failed", "woocommerce.url"},
		{"woo private address", req(t, http.MethodPost, storePath(a, "/imports"), tokOwnerA, `{"kind":"woocommerce","woocommerce":{"url":"https://192.168.1.1","consumer_key":"ck_1","consumer_secret":"cs_1"}}`), 422, "validation_failed", "woocommerce.url"},
		{"missing import", req(t, http.MethodGet, storePath(a, "/imports/"+id), tokOwnerA, ""), 404, "import_not_found", ""},
		{"bad import id", req(t, http.MethodPost, storePath(a, "/imports/nope/start"), tokOwnerA, ""), 404, "import_not_found", ""},
		{"svg upload", req(t, http.MethodPost, storePath(a, "/products/"+id+"/images"), tokOwnerA, `{"content_type":"image/svg+xml"}`), 422, "validation_failed", "content_type"},
		{"upload to missing product", req(t, http.MethodPost, storePath(a, "/products/"+id+"/images"), tokOwnerA, `{"content_type":"image/png"}`), 404, "product_not_found", ""},
		{"long alt", req(t, http.MethodPatch, storePath(a, "/products/"+id+"/images/"+id), tokOwnerA, `{"alt":"`+strings.Repeat("a", 201)+`"}`), 422, "validation_failed", "alt"},
		{"missing image", req(t, http.MethodPost, storePath(a, "/products/"+id+"/images/"+id+"/complete"), tokOwnerA, ""), 404, "image_not_found", ""},
		{"bad image id", req(t, http.MethodDelete, storePath(a, "/products/"+id+"/images/nope"), tokOwnerA, ""), 404, "image_not_found", ""},
		{"bad colour", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","options":[{"name":"اللون","values":["كحلي"],"colors":["navy"]}],"variants":[{"option_values":["كحلي"],"price_minor":1}]}`), 422, "validation_failed", "options[0].colors[0]"},
		{"colour count", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","options":[{"name":"اللون","values":["كحلي"],"colors":[]}],"variants":[{"option_values":["كحلي"],"price_minor":1}]}`), 422, "validation_failed", "options[0].colors"},
		{"malformed size chart id", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","size_chart_id":"nope","variants":[{"price_minor":1}]}`), 422, "validation_failed", "size_chart_id"},
		{"unknown size chart", req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","size_chart_id":"`+id+`","variants":[{"price_minor":1}]}`), 422, "validation_failed", "size_chart_id"},
		{"chart without rows", req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA, `{"name":"x","header":["a","b"],"body":[]}`), 422, "validation_failed", "body"},
		{"chart short row", req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA, `{"name":"x","header":["a","b"],"body":[["S"]]}`), 422, "validation_failed", "body[0]"},
		{"chart one column", req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA, `{"name":"x","header":["a"],"body":[["S"]]}`), 422, "validation_failed", "header"},
		{"chart no size", req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA, `{"name":"x","header":["a","b"],"body":[[" ","1"]]}`), 422, "validation_failed", "body[0][0]"},
		{"chart without body", req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA, ``), 400, "", ""},
		{"bad chart id", req(t, http.MethodGet, storePath(a, "/size-charts/nope"), tokOwnerA, ""), 404, "size_chart_not_found", ""},
		{"missing chart", req(t, http.MethodPut, storePath(a, "/size-charts/"+id), tokOwnerA, `{"name":"x","header":["a","b"],"body":[["S","1"]]}`), 404, "size_chart_not_found", ""},
		{"missing chart delete", req(t, http.MethodDelete, storePath(a, "/size-charts/"+id), tokOwnerA, ""), 404, "size_chart_not_found", ""},
		{"bad stock filter", req(t, http.MethodGet, storePath(a, "/products?stock=some"), tokOwnerA, ""), 422, "validation_failed", "stock"},
		{"zero low threshold", req(t, http.MethodGet, storePath(a, "/products?stock=low&low_threshold=0"), tokOwnerA, ""), 422, "validation_failed", "low_threshold"},
		{"bulk action", req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":["`+id+`"],"action":"publish"}`), 422, "validation_failed", "action"},
		{"bulk no ids", req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":[],"action":"draft"}`), 422, "validation_failed", "product_ids"},
		{"bulk bad id", req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":["nope"],"action":"draft"}`), 422, "validation_failed", "product_ids[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, body := f.do(t, tt.rq)
			if code != tt.status || (tt.code != "" && body["code"] != tt.code) {
				t.Fatalf("= %d %v; want %d %q", code, body, tt.status, tt.code)
			}
			if tt.field == "" {
				return
			}
			fields, _ := body["errors"].([]any)
			for _, fe := range fields {
				if fe.(map[string]any)["field"] == tt.field {
					return
				}
			}
			t.Fatalf("no error on field %q: %v", tt.field, body)
		})
	}
}

func TestConflicts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	_, p := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, teeBody))
	if c, body := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, strings.ReplaceAll(teeBody, "TEE-", "X-"))); c != 422 || body["errors"].([]any)[0].(map[string]any)["code"] != "taken" {
		t.Fatalf("duplicate slug = %d %v", c, body)
	}
	_, parent := f.do(t, req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"Parent"}`))
	f.do(t, req(t, http.MethodPost, storePath(a, "/categories"), tokOwnerA, `{"name":"Kid","parent_id":"`+parent["id"].(string)+`"}`))
	if c, body := f.do(t, req(t, http.MethodDelete, storePath(a, "/categories/"+parent["id"].(string)), tokOwnerA, "")); c != http.StatusConflict || body["code"] != "category_has_children" {
		t.Fatalf("delete parent = %d %v", c, body)
	}

	vid := p["variants"].([]any)[0].(map[string]any)["id"].(string)
	ctxA := tenant.WithStore(t.Context(), a)
	if _, err := f.svc.ReserveStock(ctxA, domain.ReserveRequest{Ref: "c1", Lines: []domain.ReserveLine{{VariantID: mustID(t, vid), Qty: 3}}}); err != nil {
		t.Fatal(err)
	}
	if c, body := f.do(t, req(t, http.MethodPut, storePath(a, "/variants/"+vid+"/inventory"), tokOwnerA, `{"on_hand":2}`)); c != http.StatusConflict || body["code"] != "stock_below_reserved" {
		t.Fatalf("below reserved = %d %v", c, body)
	}
}

func mustID(t *testing.T, s string) ulid.ID {
	t.Helper()
	id, err := ulid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	id := ulid.NewGenerator().New().String()
	product := `{"title":"x","variants":[{"price_minor":1}]}`
	chart := `{"name":"x","header":["a","b"],"body":[["S","1"]]}`
	tests := []struct {
		name   string
		rq     *http.Request
		status int
	}{
		{"no session", req(t, http.MethodGet, storePath(a, "/products"), "", ""), http.StatusUnauthorized},
		{"unknown token", req(t, http.MethodGet, storePath(a, "/categories"), "forged", ""), http.StatusUnauthorized},
		{"support lists products", req(t, http.MethodGet, storePath(a, "/products"), tokSupportA, ""), http.StatusOK},
		{"shipping searches products", req(t, http.MethodGet, storePath(a, "/products/search?q=x"), tokShippingA, ""), http.StatusOK},
		{"search needs a session", req(t, http.MethodGet, storePath(a, "/products/search?q=x"), "", ""), http.StatusUnauthorized},
		{"support lists categories", req(t, http.MethodGet, storePath(a, "/categories"), tokSupportA, ""), http.StatusOK},
		{"support reads product", req(t, http.MethodGet, storePath(a, "/products/"+id), tokSupportA, ""), http.StatusNotFound},
		{"support cannot create product", req(t, http.MethodPost, storePath(a, "/products"), tokSupportA, product), http.StatusForbidden},
		{"support cannot update product", req(t, http.MethodPut, storePath(a, "/products/"+id), tokSupportA, product), http.StatusForbidden},
		{"support cannot delete product", req(t, http.MethodDelete, storePath(a, "/products/"+id), tokSupportA, ""), http.StatusForbidden},
		{"support cannot set stock", req(t, http.MethodPut, storePath(a, "/variants/"+id+"/inventory"), tokSupportA, `{"on_hand":1}`), http.StatusForbidden},
		{"support cannot create category", req(t, http.MethodPost, storePath(a, "/categories"), tokSupportA, `{"name":"x"}`), http.StatusForbidden},
		{"support cannot update category", req(t, http.MethodPut, storePath(a, "/categories/"+id), tokSupportA, `{"name":"x"}`), http.StatusForbidden},
		{"support cannot delete category", req(t, http.MethodDelete, storePath(a, "/categories/"+id), tokSupportA, ""), http.StatusForbidden},
		{"shipping sets stock", req(t, http.MethodPut, storePath(a, "/variants/"+id+"/inventory"), tokShippingA, `{"on_hand":1}`), http.StatusNotFound},
		{"shipping cannot create product", req(t, http.MethodPost, storePath(a, "/products"), tokShippingA, product), http.StatusForbidden},
		{"support cannot list imports", req(t, http.MethodGet, storePath(a, "/imports"), tokSupportA, ""), http.StatusForbidden},
		{"support cannot create imports", req(t, http.MethodPost, storePath(a, "/imports"), tokSupportA, `{"kind":"csv"}`), http.StatusForbidden},
		{"shipping cannot read imports", req(t, http.MethodGet, storePath(a, "/imports/"+id), tokShippingA, ""), http.StatusForbidden},
		{"shipping cannot start imports", req(t, http.MethodPost, storePath(a, "/imports/"+id+"/start"), tokShippingA, ""), http.StatusForbidden},
		{"support cannot upload images", req(t, http.MethodPost, storePath(a, "/products/"+id+"/images"), tokSupportA, `{"content_type":"image/png"}`), http.StatusForbidden},
		{"support cannot complete images", req(t, http.MethodPost, storePath(a, "/products/"+id+"/images/"+id+"/complete"), tokSupportA, ""), http.StatusForbidden},
		{"shipping cannot edit images", req(t, http.MethodPatch, storePath(a, "/products/"+id+"/images/"+id), tokShippingA, `{"alt":"x"}`), http.StatusForbidden},
		{"shipping cannot delete images", req(t, http.MethodDelete, storePath(a, "/products/"+id+"/images/"+id), tokShippingA, ""), http.StatusForbidden},
		{"support counts products", req(t, http.MethodGet, storePath(a, "/products/counts"), tokSupportA, ""), http.StatusOK},
		{"support cannot bulk-edit products", req(t, http.MethodPost, storePath(a, "/products/bulk"), tokSupportA, `{"product_ids":["`+id+`"],"action":"draft"}`), http.StatusForbidden},
		{"shipping cannot bulk-delete products", req(t, http.MethodPost, storePath(a, "/products/bulk"), tokShippingA, `{"product_ids":["`+id+`"],"action":"delete"}`), http.StatusForbidden},
		{"support lists size charts", req(t, http.MethodGet, storePath(a, "/size-charts"), tokSupportA, ""), http.StatusOK},
		{"support reads a size chart", req(t, http.MethodGet, storePath(a, "/size-charts/"+id), tokSupportA, ""), http.StatusNotFound},
		{"size charts need a session", req(t, http.MethodGet, storePath(a, "/size-charts"), "", ""), http.StatusUnauthorized},
		{"support cannot create size charts", req(t, http.MethodPost, storePath(a, "/size-charts"), tokSupportA, chart), http.StatusForbidden},
		{"support cannot update size charts", req(t, http.MethodPut, storePath(a, "/size-charts/"+id), tokSupportA, chart), http.StatusForbidden},
		{"shipping cannot delete size charts", req(t, http.MethodDelete, storePath(a, "/size-charts/"+id), tokShippingA, ""), http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if c, body := f.do(t, tt.rq); c != tt.status {
				t.Fatalf("= %d %v; want %d", c, body, tt.status)
			}
		})
	}
}

// TestCrossTenant: store A's owner reaches for store B, by B's store ID in
// the path and by B's resource IDs under A's own path. Every attempt is a
// 404 that says nothing about B, and B's data is unchanged.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	pid, vid, cid, iid := f.productB.ID.String(), f.productB.Variants[0].ID.String(), f.catB.ID.String(), f.imageB.ID.String()
	mid, sid := f.importB.ID.String(), f.chartB.ID.String()
	leaks := []string{"B secret product", "B secret category", "B-SKU-1", "B secret alt", "b-secret-shop", "cs_bsecret", pid, vid, cid, iid, mid,
		"B secret chart", sid}
	chart := `{"name":"pwned","header":["a","b"],"body":[["S","1"]]}`
	product := `{"title":"pwned","variants":[{"price_minor":1}]}`

	unchanged := func(tb testing.TB) {
		tb.Helper()
		p, err := f.svc.GetProduct(f.ctxB, f.productB.ID)
		if err != nil || p.Title != "B secret product" || len(p.Variants) != 1 || p.Variants[0].Stock.OnHand != 7 || len(p.CategoryIDs) != 1 ||
			len(p.Images) != 1 || p.Images[0].Alt != "B secret alt" || p.Images[0].Status != domain.ImagePending {
			tb.Errorf("store B's product changed: %+v %v", p, err)
		}
		if im, imErr := f.svc.GetImport(f.ctxB, f.importB.ID); imErr != nil || im.Status != domain.ImportPending {
			tb.Errorf("store B's import changed: %+v %v", im, imErr)
		}
		if list, listErr := f.svc.ListImports(f.ctxB); listErr != nil || len(list) != 1 {
			tb.Errorf("store B's imports changed: %d %v", len(list), listErr)
		}
		cats, err := f.svc.ListCategories(f.ctxB)
		if err != nil || len(cats) != 1 || cats[0].Name != "B secret category" {
			tb.Errorf("store B's categories changed: %+v %v", cats, err)
		}
		if c, cErr := f.svc.GetSizeChart(f.ctxB, f.chartB.ID); cErr != nil || c.Name != "B secret chart" || len(c.Body) != 1 {
			tb.Errorf("store B's size chart changed: %+v %v", c, cErr)
		}
		if list, listErr := f.svc.ListSizeCharts(f.ctxB); listErr != nil || len(list) != 1 {
			tb.Errorf("store B's size charts changed: %d %v", len(list), listErr)
		}
	}

	cases := []testkit.CrossTenantCase{
		// B's store in the path.
		{OperationID: "listProducts", Request: req(t, http.MethodGet, storePath(b, "/products"), tokOwnerA, "")},
		{OperationID: "searchProducts", Request: req(t, http.MethodGet, storePath(b, "/products/search?q=B"), tokOwnerA, ""), Leaks: []string{b.String()}},
		{OperationID: "createProduct", Request: req(t, http.MethodPost, storePath(b, "/products"), tokOwnerA, product), Unchanged: unchanged},
		{OperationID: "getProduct", Request: req(t, http.MethodGet, storePath(b, "/products/"+pid), tokOwnerA, "")},
		{OperationID: "listCategories", Request: req(t, http.MethodGet, storePath(b, "/categories"), tokOwnerA, "")},
		{OperationID: "createCategory", Request: req(t, http.MethodPost, storePath(b, "/categories"), tokOwnerA, `{"name":"x"}`), Unchanged: unchanged},
		// A's store, B's resource IDs.
		{OperationID: "getProduct", Request: req(t, http.MethodGet, storePath(a, "/products/"+pid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateProduct", Request: req(t, http.MethodPut, storePath(a, "/products/"+pid), tokOwnerA, product), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateProduct", Request: req(t, http.MethodPut, storePath(b, "/products/"+pid), tokOwnerA, product), Unchanged: unchanged},
		{OperationID: "deleteProduct", Request: req(t, http.MethodDelete, storePath(a, "/products/"+pid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteProduct", Request: req(t, http.MethodDelete, storePath(b, "/products/"+pid), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "setInventory", Request: req(t, http.MethodPut, storePath(a, "/variants/"+vid+"/inventory"), tokOwnerA, `{"on_hand":0}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "setInventory", Request: req(t, http.MethodPut, storePath(b, "/variants/"+vid+"/inventory"), tokOwnerA, `{"on_hand":0}`), Unchanged: unchanged},
		{OperationID: "updateCategory", Request: req(t, http.MethodPut, storePath(a, "/categories/"+cid), tokOwnerA, `{"name":"pwned"}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateCategory", Request: req(t, http.MethodPut, storePath(b, "/categories/"+cid), tokOwnerA, `{"name":"pwned"}`), Unchanged: unchanged},
		{OperationID: "deleteCategory", Request: req(t, http.MethodDelete, storePath(a, "/categories/"+cid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteCategory", Request: req(t, http.MethodDelete, storePath(b, "/categories/"+cid), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "createImageUpload", Request: req(t, http.MethodPost, storePath(a, "/products/"+pid+"/images"), tokOwnerA, `{"content_type":"image/png"}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "createImageUpload", Request: req(t, http.MethodPost, storePath(b, "/products/"+pid+"/images"), tokOwnerA, `{"content_type":"image/png"}`), Unchanged: unchanged},
		{OperationID: "completeImageUpload", Request: req(t, http.MethodPost, storePath(a, "/products/"+pid+"/images/"+iid+"/complete"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateImage", Request: req(t, http.MethodPatch, storePath(a, "/products/"+pid+"/images/"+iid), tokOwnerA, `{"alt":"pwned"}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateImage", Request: req(t, http.MethodPatch, storePath(b, "/products/"+pid+"/images/"+iid), tokOwnerA, `{"alt":"pwned"}`), Unchanged: unchanged},
		{OperationID: "listImports", Request: req(t, http.MethodGet, storePath(b, "/imports"), tokOwnerA, "")},
		{OperationID: "createImport", Request: req(t, http.MethodPost, storePath(b, "/imports"), tokOwnerA, `{"kind":"csv"}`), Unchanged: unchanged},
		{OperationID: "getImport", Request: req(t, http.MethodGet, storePath(a, "/imports/"+mid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getImport", Request: req(t, http.MethodGet, storePath(b, "/imports/"+mid), tokOwnerA, "")},
		{OperationID: "startImport", Request: req(t, http.MethodPost, storePath(a, "/imports/"+mid+"/start"), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "startImport", Request: req(t, http.MethodPost, storePath(b, "/imports/"+mid+"/start"), tokOwnerA, ""), Unchanged: unchanged},
		{OperationID: "deleteImage", Request: req(t, http.MethodDelete, storePath(a, "/products/"+pid+"/images/"+iid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "countProducts", Request: req(t, http.MethodGet, storePath(b, "/products/counts"), tokOwnerA, "")},
		{OperationID: "bulkProducts", Request: req(t, http.MethodPost, storePath(b, "/products/bulk"), tokOwnerA, `{"product_ids":["`+pid+`"],"action":"archived"}`), Unchanged: unchanged},
		{OperationID: "listSizeCharts", Request: req(t, http.MethodGet, storePath(b, "/size-charts"), tokOwnerA, "")},
		{OperationID: "createSizeChart", Request: req(t, http.MethodPost, storePath(b, "/size-charts"), tokOwnerA, chart), Unchanged: unchanged},
		{OperationID: "getSizeChart", Request: req(t, http.MethodGet, storePath(a, "/size-charts/"+sid), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getSizeChart", Request: req(t, http.MethodGet, storePath(b, "/size-charts/"+sid), tokOwnerA, "")},
		{OperationID: "updateSizeChart", Request: req(t, http.MethodPut, storePath(a, "/size-charts/"+sid), tokOwnerA, chart), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "updateSizeChart", Request: req(t, http.MethodPut, storePath(b, "/size-charts/"+sid), tokOwnerA, chart), Unchanged: unchanged},
		{OperationID: "deleteSizeChart", Request: req(t, http.MethodDelete, storePath(a, "/size-charts/"+sid), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "deleteSizeChart", Request: req(t, http.MethodDelete, storePath(b, "/size-charts/"+sid), tokOwnerA, ""), Unchanged: unchanged},
	}
	for _, c := range cases {
		testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
	}

	// Search under A's own path is scoped to A by the handler's context.
	code, sr := f.do(t, req(t, http.MethodGet, storePath(a, "/products/search?q=secret"), tokOwnerA, ""))
	if code != http.StatusOK || sr["items"].([]any)[0].(map[string]any)["id"] != a.String() {
		t.Fatalf("search ran outside store A: %d %v", code, sr)
	}

	// Lists under A's own path never show B's rows.
	for _, path := range []string{"/products", "/products?category_id=" + cid, "/categories", "/size-charts"} {
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req(t, http.MethodGet, storePath(a, path), tokOwnerA, ""))
		for _, leak := range leaks[:3] {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s leaks %q", path, leak)
			}
		}
	}
	// A cannot attach B's category to its own product.
	code, _ = f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","category_ids":["`+cid+`"],"variants":[{"price_minor":1}]}`))
	if code != 422 {
		t.Fatalf("foreign category accepted: %d", code)
	}
	// B's products under A's path are skipped, not changed.
	for _, action := range []string{"archived", "delete"} {
		code, body := f.do(t, req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":["`+pid+`"],"action":"`+action+`"}`))
		if code != http.StatusOK || body["updated"] != float64(0) {
			t.Fatalf("bulk %s on B's product = %d %v", action, code, body)
		}
	}
	if code, body := f.do(t, req(t, http.MethodGet, storePath(a, "/products/counts"), tokOwnerA, "")); code != http.StatusOK || body["all"] != float64(0) {
		t.Fatalf("A's counts include B's: %d %v", code, body)
	}
	// Nor B's size chart.
	code, _ = f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, `{"title":"x","size_chart_id":"`+sid+`","variants":[{"price_minor":1}]}`))
	if code != 422 {
		t.Fatalf("foreign size chart accepted: %d", code)
	}
	unchanged(t)
}

// The starter template's samples (ADR-0034) are deleted in one go, only
// the caller's, and only with catalog.manage.
func TestDeleteSampleProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	ctxA := tenant.WithStore(t.Context(), a)
	sample := func(ctx context.Context, slug string) {
		t.Helper()
		if _, err := f.svc.EnsureSampleProduct(ctx, app.SampleProduct{Product: domain.ProductInput{Slug: slug, Title: "عينة", Status: "active",
			Variants: []domain.VariantInput{{PriceMinor: 100}}}}); err != nil {
			t.Fatal(err)
		}
	}
	sample(ctxA, "sample-a1")
	sample(ctxA, "sample-a2")
	sample(f.ctxB, "sample-b")
	bSamples := func(tb testing.TB) {
		tb.Helper()
		p, err := f.svc.ListProducts(f.ctxB, app.ProductQuery{})
		n := 0
		for _, it := range p.Items {
			if it.Sample {
				n++
			}
		}
		if err != nil || n != 1 {
			tb.Errorf("store B's samples = %d, %v", n, err)
		}
	}

	// The list marks them.
	code, page := f.do(t, req(t, http.MethodGet, storePath(a, "/products"), tokOwnerA, ""))
	marked := 0
	for _, it := range page["items"].([]any) {
		if it.(map[string]any)["sample"] == true {
			marked++
		}
	}
	if code != http.StatusOK || marked != 2 {
		t.Fatalf("listProducts = %d, %d marked", code, marked)
	}

	if denied, body := f.do(t, req(t, http.MethodDelete, storePath(a, "/products/samples"), tokSupportA, "")); denied != http.StatusForbidden {
		t.Fatalf("support deletes samples = %d %v", denied, body)
	}
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{OperationID: "deleteSampleProducts",
		Request: req(t, http.MethodDelete, storePath(b, "/products/samples"), tokOwnerA, ""), Unchanged: bSamples})

	code, body := f.do(t, req(t, http.MethodDelete, storePath(a, "/products/samples"), tokOwnerA, ""))
	if code != http.StatusOK || body["deleted"] != float64(2) {
		t.Fatalf("deleteSampleProducts = %d %v", code, body)
	}
	if code, body = f.do(t, req(t, http.MethodDelete, storePath(a, "/products/samples"), tokOwnerA, "")); code != http.StatusOK || body["deleted"] != float64(0) {
		t.Fatalf("again = %d %v", code, body)
	}
	bSamples(t)
}

func TestListLowStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	code, body := f.do(t, req(t, http.MethodGet, storePath(a, "/inventory/low?threshold=10&limit=5"), tokSupportA, ""))
	if code != http.StatusOK || body["total"] == nil || body["items"] == nil {
		t.Fatalf("listLowStock = %d %v", code, body)
	}
	if code, body = f.do(t, req(t, http.MethodGet, storePath(a, "/inventory/low?limit=50"), tokOwnerA, "")); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
		t.Fatalf("limit 50 = %d %v", code, body)
	}
	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{OperationID: "listLowStock",
		Request: req(t, http.MethodGet, storePath(b, "/inventory/low?threshold=10"), tokOwnerA, ""), Leaks: []string{"B secret product", "B-SKU-1"}})
}

// Size charts (ADR-0035): created, used by a product, listed with its
// product count, replaced and deleted; colours round-trip on options.
func TestSizeChartsHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, c := f.do(t, req(t, http.MethodPost, storePath(a, "/size-charts"), tokOwnerA,
		`{"name":"تيشيرتات","header":["المقاس","الصدر","الطول"],"body":[["S","96","68"],["M","102",""]]}`))
	if code != http.StatusCreated || c["name"] != "تيشيرتات" || len(c["body"].([]any)) != 2 || c["products"] != float64(0) {
		t.Fatalf("createSizeChart = %d %v", code, c)
	}
	cid := c["id"].(string)
	code, p := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA,
		`{"title":"تيشيرت","size_chart_id":"`+cid+`","options":[{"name":"اللون","values":["كحلي","أبيض"],"colors":["#1F2A44",""]}],`+
			`"variants":[{"option_values":["كحلي"],"price_minor":25000},{"option_values":["أبيض"],"price_minor":25000}]}`))
	if code != http.StatusCreated || p["size_chart_id"] != cid {
		t.Fatalf("createProduct = %d %v", code, p)
	}
	colors := p["options"].([]any)[0].(map[string]any)["colors"].([]any)
	if colors[0] != "#1f2a44" || colors[1] != "" {
		t.Fatalf("colors = %v", colors)
	}
	if code, list := f.do(t, req(t, http.MethodGet, storePath(a, "/size-charts"), tokOwnerA, "")); code != http.StatusOK ||
		len(list["items"].([]any)) != 1 || list["items"].([]any)[0].(map[string]any)["products"] != float64(1) {
		t.Fatalf("listSizeCharts = %d %v", code, list)
	}
	if code, got := f.do(t, req(t, http.MethodPut, storePath(a, "/size-charts/"+cid), tokOwnerA,
		`{"name":"تيشيرتات رجالي","header":["المقاس","الصدر"],"body":[["L","108"]]}`)); code != http.StatusOK || got["name"] != "تيشيرتات رجالي" || got["products"] != float64(1) {
		t.Fatalf("updateSizeChart = %d %v", code, got)
	}
	if code, got := f.do(t, req(t, http.MethodGet, storePath(a, "/size-charts/"+cid), tokOwnerA, "")); code != http.StatusOK || len(got["header"].([]any)) != 2 {
		t.Fatalf("getSizeChart = %d %v", code, got)
	}
	if code, _ := f.do(t, req(t, http.MethodDelete, storePath(a, "/size-charts/"+cid), tokOwnerA, "")); code != http.StatusNoContent {
		t.Fatalf("deleteSizeChart = %d", code)
	}
	if code, got := f.do(t, req(t, http.MethodGet, storePath(a, "/products/"+p["id"].(string)), tokOwnerA, "")); code != http.StatusOK || got["size_chart_id"] != nil {
		t.Fatalf("product keeps a deleted chart: %d %v", code, got)
	}
}

// The products list's tabs, stock filter and bulk bar (U5b).
func TestProductListTools(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	var ids []string
	for _, body := range []string{
		`{"title":"plenty","status":"active","variants":[{"price_minor":100,"on_hand":40}]}`,
		`{"title":"few","status":"active","variants":[{"price_minor":100,"on_hand":2}]}`,
		`{"title":"none","status":"draft","variants":[{"price_minor":100,"on_hand":0}]}`,
	} {
		code, p := f.do(t, req(t, http.MethodPost, storePath(a, "/products"), tokOwnerA, body))
		if code != http.StatusCreated {
			t.Fatalf("create = %d %v", code, p)
		}
		ids = append(ids, p["id"].(string))
	}
	if code, c := f.do(t, req(t, http.MethodGet, storePath(a, "/products/counts"), tokOwnerA, "")); code != http.StatusOK ||
		c["all"] != float64(3) || c["active"] != float64(2) || c["draft"] != float64(1) || c["archived"] != float64(0) {
		t.Fatalf("counts = %d %v", code, c)
	}
	for q, want := range map[string]int{"stock=out": 1, "stock=low": 1, "stock=low&low_threshold=1": 0, "stock=in_stock": 2} {
		if code, page := f.do(t, req(t, http.MethodGet, storePath(a, "/products?"+q), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != want {
			t.Fatalf("%s = %d %v, want %d", q, code, page, want)
		}
	}
	code, res := f.do(t, req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":["`+ids[0]+`","`+ids[1]+`"],"action":"archived"}`))
	if code != http.StatusOK || res["updated"] != float64(2) {
		t.Fatalf("bulk archive = %d %v", code, res)
	}
	if code, page := f.do(t, req(t, http.MethodGet, storePath(a, "/products?status=archived"), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != 2 {
		t.Fatalf("archived = %d %v", code, page)
	}
	if code, res = f.do(t, req(t, http.MethodPost, storePath(a, "/products/bulk"), tokOwnerA, `{"product_ids":["`+ids[2]+`"],"action":"delete"}`)); code != http.StatusOK || res["updated"] != float64(1) {
		t.Fatalf("bulk delete = %d %v", code, res)
	}
	if code, _ := f.do(t, req(t, http.MethodGet, storePath(a, "/products/"+ids[2]), tokOwnerA, "")); code != http.StatusNotFound {
		t.Fatalf("deleted product = %d", code)
	}
}
