package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

func testImports(t *testing.T) app.Imports {
	t.Helper()
	// Public development-only key (see .env.example).
	kr, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=")
	if err != nil {
		t.Fatal(err)
	}
	return app.Imports{Keyring: kr, HTTP: safehttp.New(safehttp.Options{}),
		Retry: retry.Policy{MaxAttempts: 2, Initial: time.Millisecond, Max: time.Millisecond}}
}

// runImports runs the store's queued import jobs, as the worker would.
func runImports(ctx context.Context, t *testing.T, f fixture, svc *app.Service) {
	t.Helper()
	for _, a := range takeJobs[app.RunImportArgs](ctx, t, f) {
		if err := (&app.ImportWorker{Svc: svc}).Work(ctx, &river.Job[app.RunImportArgs]{Args: a}); err != nil {
			t.Fatalf("import job: %v", err)
		}
	}
}

// fileImport creates a CSV import, "uploads" data and runs it.
func fileImport(ctx context.Context, t *testing.T, f fixture, data string) domain.Import {
	t.Helper()
	im, up, err := f.svc.CreateImport(ctx, app.NewImportInput{Kind: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	if up == nil || up.MaxBytes != domain.MaxImportFileBytes || up.Fields["Content-Type"] != "text/csv" {
		t.Fatalf("upload = %+v", up)
	}
	if data != "" {
		_ = f.objects.Put(ctx, im.SourceKey, "text/csv", []byte(data))
	}
	if im, err = f.svc.StartImport(ctx, im.ID); err != nil || im.Status != domain.ImportQueued {
		t.Fatalf("start: %v %+v", err, im)
	}
	runImports(ctx, t, f, f.svc)
	if im, err = f.svc.GetImport(ctx, im.ID); err != nil {
		t.Fatal(err)
	}
	return im
}

// catalogCSV has n products; every tenth has three size variants.
func catalogCSV(n int) string {
	var b strings.Builder
	b.WriteString("handle,title,categories,status,option1_name,option1_value,sku,price,stock\n")
	for i := range n {
		h := fmt.Sprintf("p-%04d", i)
		if i%10 == 0 {
			for j, size := range []string{"S", "M", "L"} {
				title := ""
				if j == 0 {
					title = "منتج مستورد " + strconv.Itoa(i)
				}
				fmt.Fprintf(&b, "%s,%s,مستورد,active,المقاس,%s,%s-%s,%d.50,3\n", h, title, size, h, size, 100+i)
			}
			continue
		}
		fmt.Fprintf(&b, "%s,Imported %d,مستورد | Extra,active,,,%s,%d,%d\n", h, i, h, 50+i, i%7)
	}
	return b.String()
}

func countProducts(ctx context.Context, t *testing.T, f fixture) int {
	t.Helper()
	var n int
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE deleted_at IS NULL`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestImport1000Products is the P2 acceptance test: a 1,000-product CSV
// completes as a job; running it again updates instead of duplicating.
func TestImport1000Products(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	data := catalogCSV(1000)
	start := time.Now()
	im := fileImport(ctx, t, f, data)
	t.Logf("1,000 products imported in %s", time.Since(start).Round(time.Millisecond))
	if im.Status != domain.ImportSucceeded || im.Total != 1000 || im.Cursor != 1000 || im.Created != 1000 || im.Failed != 0 || im.FinishedAt == nil {
		t.Fatalf("import = %+v", im)
	}
	if n := countProducts(ctx, t, f); n != 1000 {
		t.Fatalf("products = %d, want 1000", n)
	}
	cats, err := f.svc.ListCategories(ctx)
	if err != nil || len(cats) != 2 {
		t.Fatalf("categories created = %d (%v), want 2", len(cats), err)
	}
	if got := takeJobs[app.IndexProductArgs](ctx, t, f); len(got) != 1000 {
		t.Fatalf("index jobs = %d, want 1000", len(got))
	}
	runJobs(ctx, t, f) // deletes the uploaded file
	if f.objects.has(im.SourceKey) {
		t.Fatal("uploaded file kept after the import")
	}

	page, err := f.svc.ListProducts(ctx, app.ProductQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var before domain.Product
	for _, p := range page.Items {
		if p.Slug == "p-0990" {
			before = p
		}
	}
	if len(before.Variants) != 3 || before.Variants[0].Price.Amount() != 109050 || before.Options[0].Name != "المقاس" {
		t.Fatalf("p-0990 = %+v", before)
	}

	again := fileImport(ctx, t, f, data)
	if again.Status != domain.ImportSucceeded || again.Created != 0 || again.Updated != 1000 || again.Failed != 0 {
		t.Fatalf("re-import = %+v", again)
	}
	if n := countProducts(ctx, t, f); n != 1000 {
		t.Fatalf("re-import duplicated: %d products", n)
	}
	after, err := f.svc.GetProduct(ctx, before.ID)
	if err != nil || after.Variants[0].ID != before.Variants[0].ID {
		t.Fatalf("variants matched by SKU lost their identity: %v", err)
	}
}

func TestImportReport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	var b strings.Builder
	b.WriteString("title,price,status\nGood,10,active\n")
	for i := range domain.MaxImportRowErrors + 5 {
		fmt.Fprintf(&b, "Bad %d,not-a-price,\n", i)
	}
	b.WriteString(",5,\n")        // no title
	b.WriteString("Odd,5,live\n") // unknown status
	im := fileImport(ctx, t, f, b.String())
	if im.Status != domain.ImportSucceeded || im.Created != 1 || im.Failed != domain.MaxImportRowErrors+7 || len(im.RowErrors) != domain.MaxImportRowErrors {
		t.Fatalf("import = %d created, %d failed, %d errors kept", im.Created, im.Failed, len(im.RowErrors))
	}
	if e := im.RowErrors[0]; e.Line != 3 || !strings.Contains(e.Message, "price") {
		t.Fatalf("first row error = %+v", e)
	}

	for data, reason := range map[string]string{"sku,qty\n1,2\n": "bad_header", "": "upload_missing", "title,price\n\"x,1\n": "csv_malformed"} {
		failed := fileImport(ctx, t, f, data)
		if failed.Status != domain.ImportFailed || failed.FailureReason != reason {
			t.Errorf("%q: %s %q, want failed %q", data, failed.Status, failed.FailureReason, reason)
		}
	}

	_, err := f.svc.StartImport(ctx, im.ID)
	if !errs.Is(err, errs.KindConflict) {
		t.Fatalf("starting twice: %v", err)
	}
	_, _, err = f.svc.CreateImport(ctx, app.NewImportInput{Kind: "pdf"})
	wantField(t, err, "kind", "invalid")
	_, err = f.svc.GetImport(ctx, f.ids.New())
	wantKind(t, err, errs.KindNotFound)
	list, err := f.svc.ListImports(ctx)
	if err != nil || len(list) != 4 || list[0].ID.Time().Before(list[3].ID.Time()) {
		t.Fatalf("list = %d %v", len(list), err)
	}
}

// TestImportResumes: a retried job skips what it already handled.
func TestImportResumes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	im, _, err := f.svc.CreateImport(ctx, app.NewImportInput{Kind: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.objects.Put(ctx, im.SourceKey, "text/csv", []byte(catalogCSV(30)))
	if _, err = f.svc.StartImport(ctx, im.ID); err != nil {
		t.Fatal(err)
	}
	// As if a first attempt crashed after 12 records.
	if err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `UPDATE catalog_imports SET status = 'running', cursor = 12, created_count = 12 WHERE id = $1`, im.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	runImports(ctx, t, f, f.svc)
	got, _ := f.svc.GetImport(ctx, im.ID)
	if got.Status != domain.ImportSucceeded || got.Created != 30 || got.Cursor != 30 || countProducts(ctx, t, f) != 18 {
		t.Fatalf("resumed import = %+v, %d products (want 18 new)", got, countProducts(ctx, t, f))
	}
}

func fakeWooServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /wp-json/wc/v3/products", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "ck_live" || p != "cs_live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-WP-Total", "2")
		w.Header().Set("X-WP-TotalPages", "1")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "Woo Tee", "slug": "woo-tee", "type": "simple", "status": "publish", "sku": "W1",
				"regular_price": "99.99", "categories": []map[string]any{{"name": "Woo Cat"}}},
			{"id": 2, "name": "Woo Mug", "slug": "woo-mug", "type": "simple", "status": "draft", "regular_price": "20"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestWooImport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	srv := fakeWooServer(t)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	deps := f.deps
	deps.Imports.AllowHTTP = true
	deps.Imports.URLRules = safehttp.Options{AllowedPorts: []int{port}, AllowPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	deps.Imports.HTTP = safehttp.New(deps.Imports.URLRules)
	svc := app.NewService(deps)

	run := func(key, secret string) domain.Import {
		im, up, err := svc.CreateImport(ctx, app.NewImportInput{Kind: "woocommerce", WooURL: srv.URL + "/", WooKey: key, WooSecret: secret})
		if err != nil || up != nil {
			t.Fatalf("create: %v (upload %v)", err, up)
		}
		var sealed []byte
		_ = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT woo_credentials FROM catalog_imports WHERE id = $1`, im.ID).Scan(&sealed)
		})
		if bytes.Contains(sealed, []byte(secret)) || bytes.Contains(sealed, []byte(key)) {
			t.Fatal("WooCommerce keys stored in the clear")
		}
		if _, err = svc.StartImport(ctx, im.ID); err != nil {
			t.Fatal(err)
		}
		runImports(ctx, t, f, svc)
		im, _ = svc.GetImport(ctx, im.ID)
		return im
	}
	im := run("ck_live", "cs_live")
	if im.Status != domain.ImportSucceeded || im.Created != 2 || im.Total != 2 || im.WooURL != srv.URL {
		t.Fatalf("woo import = %+v", im)
	}
	if bad := run("ck_old", "cs_old"); bad.Status != domain.ImportFailed || bad.FailureReason != "woo_auth" {
		t.Fatalf("revoked keys = %+v", bad)
	}

	// The default service refuses plain HTTP, private addresses and odd keys.
	for _, in := range []app.NewImportInput{
		{WooURL: "http://shop.example.com", WooKey: "ck_1", WooSecret: "cs_1"},
		{WooURL: "https://10.0.0.5", WooKey: "ck_1", WooSecret: "cs_1"},
		{WooURL: "https://169.254.169.254", WooKey: "ck_1", WooSecret: "cs_1"},
		{WooURL: "https://shop.example.com/?x=1", WooKey: "ck_1", WooSecret: "cs_1"},
	} {
		in.Kind = "woocommerce"
		_, _, err := f.svc.CreateImport(ctx, in)
		wantField(t, err, "woocommerce.url", "invalid")
	}
	_, _, err := f.svc.CreateImport(ctx, app.NewImportInput{Kind: "woocommerce", WooURL: "https://shop.example.com", WooKey: "key", WooSecret: "secret"})
	wantField(t, err, "woocommerce.consumer_key", "invalid")
	wantField(t, err, "woocommerce.consumer_secret", "invalid")
}

func TestImportsCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	imB, _, err := f.svc.CreateImport(f.ctxB, app.NewImportInput{Kind: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.GetImport(f.ctxA, imB.ID)
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.StartImport(f.ctxA, imB.ID)
	wantKind(t, err, errs.KindNotFound)
	// A's worker running B's import ID finds nothing.
	args := app.RunImportArgs{ImportID: imB.ID}
	args.StoreID = mustStore(f.ctxA, t)
	if err = (&app.ImportWorker{Svc: f.svc}).Work(f.ctxA, &river.Job[app.RunImportArgs]{Args: args}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetImport(f.ctxB, imB.ID); got.Status != domain.ImportPending {
		t.Fatalf("B's import changed: %+v", got)
	}
	if list, _ := f.svc.ListImports(f.ctxA); len(list) != 0 {
		t.Fatalf("A lists B's imports: %d", len(list))
	}
}
