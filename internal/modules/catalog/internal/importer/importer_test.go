package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
)

const sampleCSV = "handle,title,description,status,categories,option1_name,option1_value,sku,price,compare_at_price,stock,tracked\n" +
	"tee,Basic Tee,Soft cotton,active,Men | Shirts,Size,S,TEE-S,150.00,200,5,yes\n" +
	"tee,,,,,,M,TEE-M,160,,3,\n" +
	"\n" +
	",Gift card,,draft,,,,GIFT,٥٠,,0,no\n" +
	"mug,Mug,,active,,,,MUG,abc,,1,\n"

func all(t *testing.T, s Source) []domain.ImportRecord {
	t.Helper()
	var out []domain.ImportRecord
	for {
		r, err := s.Next(t.Context())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
}

func TestCSV(t *testing.T) {
	t.Parallel()
	src, err := NewCSV([]byte("\xEF\xBB\xBF"+sampleCSV), money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	if src.Total() != 3 {
		t.Fatalf("total = %d, want 3", src.Total())
	}
	recs := all(t, src)
	tee := recs[0]
	if tee.Err != "" || tee.Line != 2 || tee.Product.Slug != "tee" || tee.Product.Status != "active" || len(tee.Product.Variants) != 2 {
		t.Fatalf("tee = %+v", tee)
	}
	if o := tee.Product.Options; len(o) != 1 || o[0].Name != "Size" || strings.Join(o[0].Values, ",") != "S,M" {
		t.Fatalf("options = %+v", o)
	}
	v0, v1 := tee.Product.Variants[0], tee.Product.Variants[1]
	if v0.PriceMinor != 15000 || *v0.CompareAtMinor != 20000 || v0.OnHand != 5 || !*v0.Tracked || v1.PriceMinor != 16000 || v1.Tracked != nil {
		t.Fatalf("variants = %+v %+v", v0, v1)
	}
	if strings.Join(tee.Categories, ",") != "Men,Shirts" {
		t.Fatalf("categories = %q", tee.Categories)
	}
	if _, err := domain.ValidateProduct(tee.Product); err != nil {
		t.Fatalf("tee does not validate: %v", err)
	}
	gift := recs[1]
	if gift.Err != "" || gift.Line != 5 || gift.Product.Variants[0].PriceMinor != 5000 || *gift.Product.Variants[0].Tracked {
		t.Fatalf("gift = %+v", gift)
	}
	if mug := recs[2]; !strings.Contains(mug.Err, "line 6: price") {
		t.Fatalf("bad price should be a row error: %+v", mug)
	}
}

func TestCSVDialectsAndArabicHeaders(t *testing.T) {
	t.Parallel()
	data := "الاسم;السعر;الكمية;التصنيف;الحالة\nقميص;١٢٠٫٥٠;٧;رجالي؛ قمصان;منشور\n"
	src, err := NewCSV([]byte(data), money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	r := all(t, src)[0]
	if r.Err != "" || r.Product.Title != "قميص" || r.Product.Variants[0].PriceMinor != 12050 || r.Product.Variants[0].OnHand != 7 ||
		r.Product.Status != "active" || len(r.Categories) != 2 {
		t.Fatalf("record = %+v", r)
	}
	tab := "title\tprice\nA\t1\n"
	if src, err = NewCSV([]byte(tab), money.SAR); err != nil || all(t, src)[0].Product.Variants[0].PriceMinor != 100 {
		t.Fatalf("tab separated: %v", err)
	}
}

func TestCSVSourceErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		code string
	}{
		{"no price column", "title,sku\nA,1\n", "bad_header"},
		{"no title column", "sku,price\n1,2\n", "bad_header"},
		{"empty", "", "empty_file"},
		{"bad quotes", "title,price\n\"A,1\n", "csv_malformed"},
		{"not utf8", "title,price\n\xff\xfe,1\n", "not_utf8"},
		{"too big", strings.Repeat("x", domain.MaxImportFileBytes+1), "file_too_large"},
	}
	for _, tt := range tests {
		_, err := NewCSV([]byte(tt.data), money.EGP)
		if code, ok := IsSourceError(err); !ok || code != tt.code {
			t.Errorf("%s: err = %v, want %s", tt.name, err, tt.code)
		}
	}
	var many strings.Builder
	many.WriteString("title,price\n")
	for i := range domain.MaxImportRecords + 1 {
		fmt.Fprintf(&many, "P%d,1\n", i)
	}
	if _, err := NewCSV([]byte(many.String()), money.EGP); err == nil {
		t.Error("more than MaxImportRecords products accepted")
	}
}

func TestSkipResumes(t *testing.T) {
	t.Parallel()
	src, err := NewCSV([]byte(sampleCSV), money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	if err = src.Skip(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := all(t, src); len(r) != 2 || r[0].Line != 5 {
		t.Fatalf("after skip: %+v", r)
	}
	if err = src.Skip(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("skip at end: %v", err)
	}
}

func TestStripHTML(t *testing.T) {
	t.Parallel()
	in := "<p>قطن <strong>100%</strong> &amp; ناعم</p><ul><li>S</li><li>M</li></ul><br/><><script>x</script>"
	if got := StripHTML(in); got != "قطن 100% & ناعم\nS\nM\nx" {
		t.Fatalf("StripHTML = %q", got)
	}
}

// FuzzCSV: any file yields either a source error or records that are
// row errors or products; the parser never panics and respects limits.
func FuzzCSV(f *testing.F) {
	f.Add([]byte(sampleCSV))
	f.Add([]byte("title;price\nA;1,5\n"))
	f.Add([]byte("\"title\",\"price\"\n\"a\"\"b\",\"1\"\n"))
	f.Add([]byte("title,price,option1_name,option1_value\nA,1,,x\nA,1,Size,\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		src, err := NewCSV(data, money.EGP)
		if err != nil {
			if _, ok := IsSourceError(err); !ok {
				t.Fatalf("non-source error: %v", err)
			}
			return
		}
		n := 0
		for {
			r, err := src.Next(context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			n++
			if r.Err == "" && len(r.Product.Variants) == 0 {
				t.Fatalf("record without variants or error: %+v", r)
			}
		}
		if n != src.Total() || n > domain.MaxImportRecords {
			t.Fatalf("records %d, total %d", n, src.Total())
		}
	})
}

// FuzzStripHTML: hostile HTML never panics.
func FuzzStripHTML(f *testing.F) {
	for _, s := range []string{"<p>a</p>", "<", "<>", "</>", "a<b", "&amp;&#x1F600;", "<h1 class='x'>t</h1>"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) { _ = StripHTML(s) })
}

// fakeWoo serves a small WooCommerce catalog; status forces errors.
type fakeWoo struct {
	failOnce atomic.Bool
	calls    atomic.Int32
}

func (fw *fakeWoo) handler() http.Handler {
	products := []map[string]any{
		{"id": 1, "name": "Tee &amp; Co", "slug": "tee", "type": "simple", "status": "publish", "description": "<p>Cotton</p>",
			"sku": "T1", "regular_price": "200", "sale_price": "150", "manage_stock": true, "stock_quantity": 4,
			"categories": []map[string]any{{"name": "Men"}, {"name": "Uncategorized"}}},
		{"id": 2, "name": "Hoodie", "slug": "hoodie", "type": "variable", "status": "draft",
			"attributes": []map[string]any{{"name": "Color", "options": []string{"Black", "White"}, "variation": true}}},
		{"id": 3, "name": "Free", "slug": "free", "type": "simple", "status": "publish", "regular_price": ""},
		{"id": 4, "name": "Bundle", "slug": "bundle", "type": "grouped", "status": "publish"},
		{"id": 5, "name": "Last", "slug": "last", "type": "simple", "status": "publish", "regular_price": "10", "manage_stock": false},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /wp-json/wc/v3/products", func(w http.ResponseWriter, r *http.Request) {
		fw.calls.Add(1)
		if user, pass, ok := r.BasicAuth(); !ok || user != "ck_test" || pass != "cs_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if fw.failOnce.CompareAndSwap(true, false) {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		from := min((page-1)*per, len(products))
		to := min(from+per, len(products))
		w.Header().Set("X-WP-Total", strconv.Itoa(len(products)))
		w.Header().Set("X-WP-TotalPages", strconv.Itoa((len(products)+per-1)/per))
		_ = json.NewEncoder(w).Encode(products[from:to])
	})
	mux.HandleFunc("GET /wp-json/wc/v3/products/2/variations", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"sku": "H-B", "regular_price": "500", "manage_stock": true, "stock_quantity": -2,
				"attributes": []map[string]any{{"name": "Color", "option": "Black"}}},
			{"sku": "H-W", "regular_price": "500", "sale_price": "450", "manage_stock": "parent",
				"attributes": []map[string]any{{"name": "color", "option": "White"}}},
		})
	})
	return mux
}

func wooClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return safehttp.New(safehttp.Options{AllowedPorts: []int{port}, AllowPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
}

func TestWoo(t *testing.T) {
	t.Parallel()
	fw := &fakeWoo{}
	srv := httptest.NewServer(fw.handler())
	t.Cleanup(srv.Close)
	cfg := WooConfig{BaseURL: srv.URL + "/", Key: "ck_test", Secret: "cs_test", Client: wooClient(t, srv), Currency: money.EGP,
		PerPage: 2, AllowHTTP: true, Retry: retry.Policy{MaxAttempts: 3, Initial: time.Millisecond, Max: time.Millisecond}}
	src, err := OpenWoo(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if src.Total() != 5 {
		t.Fatalf("total = %d", src.Total())
	}
	fw.failOnce.Store(true) // the next page fetch hits a 502 once and is retried
	recs := all(t, src)
	if len(recs) != 5 {
		t.Fatalf("records = %d", len(recs))
	}
	tee := recs[0]
	if tee.Product.Title != "Tee & Co" || tee.Product.Status != "active" || tee.Product.Description != "Cotton" ||
		tee.Product.Variants[0].PriceMinor != 15000 || *tee.Product.Variants[0].CompareAtMinor != 20000 ||
		tee.Product.Variants[0].OnHand != 4 || strings.Join(tee.Categories, ",") != "Men" {
		t.Fatalf("tee = %+v", tee)
	}
	hoodie := recs[1].Product
	if hoodie.Status != "draft" || len(hoodie.Variants) != 2 || hoodie.Variants[0].OnHand != 0 ||
		hoodie.Variants[1].PriceMinor != 45000 || *hoodie.Variants[1].Tracked || hoodie.Variants[1].OptionValues[0] != "White" {
		t.Fatalf("hoodie = %+v", hoodie)
	}
	if _, err = domain.ValidateProduct(hoodie); err != nil {
		t.Fatalf("hoodie does not validate: %v", err)
	}
	if !strings.Contains(recs[2].Err, "no regular price") || !strings.Contains(recs[3].Err, "not supported") || recs[4].Line != 5 {
		t.Fatalf("row errors = %q %q, last line %d", recs[2].Err, recs[3].Err, recs[4].Line)
	}

	// Resume: skip what was done, continue from there.
	again, err := OpenWoo(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = again.Skip(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if rest := all(t, again); len(rest) != 2 || rest[1].Product.Slug != "last" {
		t.Fatalf("resumed = %+v", rest)
	}

	bad := cfg
	bad.Secret = "wrong"
	if _, err = OpenWoo(t.Context(), bad); func() bool { c, ok := IsSourceError(err); return !ok || c != "woo_auth" }() {
		t.Fatalf("bad keys: %v", err)
	}
	insecure := cfg
	insecure.AllowHTTP = false
	if _, err = OpenWoo(t.Context(), insecure); func() bool { c, ok := IsSourceError(err); return !ok || c != "woo_bad_url" }() {
		t.Fatalf("http allowed in production mode: %v", err)
	}
	// The production client refuses loopback: a merchant cannot point us at our own network.
	internal := cfg
	internal.Client = safehttp.New(safehttp.Options{})
	if _, err = OpenWoo(t.Context(), internal); func() bool { c, ok := IsSourceError(err); return !ok || c != "woo_blocked_address" }() {
		t.Fatalf("loopback reached: %v", err)
	}
}

// TestDashboardTemplate: the CSV the dashboard offers merchants as a
// starting point imports cleanly.
func TestDashboardTemplate(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "web", "dashboard", "public", "products-template.csv"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewCSV(data, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	recs := all(t, src)
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	for _, r := range recs {
		if r.Err != "" {
			t.Fatalf("line %d: %s", r.Line, r.Err)
		}
		if _, err := domain.ValidateProduct(r.Product); err != nil {
			t.Fatalf("%s does not validate: %v", r.Product.Slug, err)
		}
	}
	if tee := recs[0].Product; len(tee.Variants) != 3 || len(tee.Options) != 2 {
		t.Fatalf("tee = %d variants, %d options", len(tee.Variants), len(tee.Options))
	}
}
