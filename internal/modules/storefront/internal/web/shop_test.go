package web_test

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var gen = ulid.NewGenerator()

func egp(minor int64) money.Money {
	m, err := money.New(minor, money.EGP)
	if err != nil {
		panic(err)
	}
	return m
}

// fakeCatalog serves a fixed set of products and counts calls.
type fakeCatalog struct {
	mu       sync.Mutex
	products []catalog.StorefrontProduct // newest first
	cats     []catalog.Category
	calls    map[string]int
}

func (c *fakeCatalog) count(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[name]++
}

func (c *fakeCatalog) calls0(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[name]
}

func (c *fakeCatalog) ProductBySlug(_ context.Context, slug string) (catalog.StorefrontProduct, error) {
	c.count("bySlug")
	for _, p := range c.products {
		if p.Slug == slug {
			return p, nil
		}
	}
	return catalog.StorefrontProduct{}, catalog.ErrProductNotFound
}

func (c *fakeCatalog) ProductsByIDs(_ context.Context, ids []ulid.ID) ([]catalog.StorefrontProduct, error) {
	c.count("byIDs")
	var out []catalog.StorefrontProduct
	for _, id := range ids {
		for _, p := range c.products {
			if p.ID == id {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListProducts(_ context.Context, q catalog.StorefrontQuery) (catalog.StorefrontPage, error) {
	c.count("list")
	var items []catalog.StorefrontProduct
	for _, p := range c.products {
		if q.CategoryID == nil || containsID(p.CategoryIDs, *q.CategoryID) {
			items = append(items, p)
		}
	}
	page := catalog.StorefrontPage{}
	if q.Cursor != "" {
		for i, p := range items {
			if p.ID.String() == q.Cursor {
				items = items[i+1:]
				break
			}
		}
	}
	if len(items) > q.Limit {
		items = items[:q.Limit]
		page.NextCursor = items[len(items)-1].ID.String()
	}
	page.Items = items
	return page, nil
}

// SellableVariants sells every available variant of the catalog's
// products.
func (c *fakeCatalog) SellableVariants(_ context.Context, ids []ulid.ID) ([]catalog.SellableVariant, error) {
	c.count("SellableVariants")
	var out []catalog.SellableVariant
	for _, id := range ids {
		for _, pr := range c.products {
			for _, v := range pr.Variants {
				if v.ID == id && v.Available {
					out = append(out, catalog.SellableVariant{VariantID: v.ID, ProductID: pr.ID, ProductTitle: pr.Title,
						ProductSlug: pr.Slug, OptionValues: v.OptionValues, Price: v.Price})
				}
			}
		}
	}
	return out, nil
}

func (c *fakeCatalog) Categories(context.Context) ([]catalog.Category, error) {
	c.count("categories")
	return c.cats, nil
}

func containsID(ids []ulid.ID, id ulid.ID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

type fakeStores struct{ store tenancy.Store }

func (f fakeStores) GetStore(context.Context) (tenancy.Store, error) { return f.store, nil }

// fakeContent holds a store's look and pages.
type fakeContent struct {
	mu       sync.Mutex
	shop     app.ShopContent
	home     domain.Page
	landings map[string]domain.Page
	policies map[string]domain.Page
	err      error
}

func (f *fakeContent) ShopContent(context.Context) (app.ShopContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shop, f.err
}

func (f *fakeContent) GetHome(context.Context) (domain.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.home, nil
}

func (f *fakeContent) PublishedLanding(_ context.Context, slug string) (domain.Page, error) {
	if p, ok := f.landings[slug]; ok {
		return p, nil
	}
	return domain.Page{}, app.ErrPageNotFound
}

func (f *fakeContent) PublishedPolicy(_ context.Context, slug string) (domain.Page, error) {
	if p, ok := f.policies[slug]; ok {
		return p, nil
	}
	return domain.Page{}, app.ErrPageNotFound
}

// fakeCarts keeps carts in memory, keyed by token.
type fakeCarts struct {
	mu    sync.Mutex
	carts map[authtoken.Token]map[ulid.ID]int
	price map[ulid.ID]catalog.StorefrontProduct
	err   error
}

func (c *fakeCarts) AddToCart(_ context.Context, tok authtoken.Token, id ulid.ID, qty int) (authtoken.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return "", c.err
	}
	if _, ok := c.price[id]; !ok {
		return "", app.ErrVariantUnavailable
	}
	if qty < 1 || qty > 20 {
		return "", domain.ValidateQty(qty, false)
	}
	if _, ok := c.carts[tok]; !ok || tok == "" {
		tok, _ = authtoken.New()
		c.carts[tok] = map[ulid.ID]int{}
	}
	c.carts[tok][id] += qty
	return tok, nil
}

func (c *fakeCarts) UpdateCartLine(_ context.Context, tok authtoken.Token, id ulid.ID, qty int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cart, ok := c.carts[tok]; ok {
		if qty == 0 {
			delete(cart, id)
		} else {
			cart[id] = qty
		}
	}
	return nil
}

func (c *fakeCarts) Cart(_ context.Context, tok authtoken.Token) (app.CartView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := app.CartView{Lines: []app.CartLineView{}}
	for id, qty := range c.carts[tok] {
		pr := c.price[id]
		var variant catalog.StorefrontVariant
		for _, pv := range pr.Variants {
			if pv.ID == id {
				variant = pv
			}
		}
		total, _ := variant.Price.MulInt(int64(qty))
		v.Lines = append(v.Lines, app.CartLineView{SellableVariant: catalog.SellableVariant{VariantID: id, ProductTitle: pr.Title,
			ProductSlug: pr.Slug, OptionValues: variant.OptionValues, Price: variant.Price}, Qty: qty, Total: total, Available: true})
		v.Units += qty
		v.Subtotal = &total
	}
	return v, nil
}

func (c *fakeCarts) ClearCart(_ context.Context, tok authtoken.Token) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.carts, tok)
	return nil
}

func (c *fakeCarts) CartUnits(ctx context.Context, tok authtoken.Token) (int, error) {
	v, err := c.Cart(ctx, tok)
	return v.Units, err
}

// fakeOrders places every order as IFT-1001 unless told to fail, and
// keeps what was asked (when log is set).
type fakeOrders struct {
	log *orderLog
}

type orderLog struct {
	mu   sync.Mutex
	reqs []web.OrderRequest
	err  error // returned by the next PlaceOrder
}

func (f fakeOrders) PlaceOrder(_ context.Context, req web.OrderRequest) (string, error) {
	if f.log == nil {
		return "IFT-1001", nil
	}
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	f.log.reqs = append(f.log.reqs, req)
	if err := f.log.err; err != nil {
		f.log.err = nil
		return "", err
	}
	return "IFT-1001", nil
}

func (f fakeOrders) placed() []web.OrderRequest {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	return append([]web.OrderRequest(nil), f.log.reqs...)
}

func (fakeOrders) OrderSummary(_ context.Context, ref string) (web.OrderSummary, error) {
	if ref != "IFT-1001" {
		return web.OrderSummary{}, web.ErrOrderNotFound
	}
	return web.OrderSummary{Ref: ref, Code: ref, Lines: []web.OrderLine{{Title: "تيشيرت قطن", Qty: 2, Total: egp(30000)}}, Total: egp(30000)}, nil
}

type fakeSigner struct{}

func (fakeSigner) URL(key string, size int, format string) string {
	return "https://img.test/" + key + "/" + format + "/" + string(rune('0'+size%10))
}

type fixture struct {
	carts   *fakeCarts
	shop    *web.Shop
	h       http.Handler
	catalog *fakeCatalog
	content *fakeContent
	store   tenant.StoreID
	shirt   catalog.StorefrontProduct
	cat     catalog.Category
}

func product(title, slug string, price int64, available bool, cats ...ulid.ID) catalog.StorefrontProduct {
	return catalog.StorefrontProduct{
		ID: gen.New(), Slug: slug, Title: title, Description: "قطن مصري ١٠٠٪.\n\nيغسل في الغسالة.",
		CategoryIDs: cats,
		Variants:    []catalog.StorefrontVariant{{ID: gen.New(), Price: egp(price), Available: available}},
		Images: []catalog.StorefrontImage{{Alt: "", Width: 800, Height: 800, Renditions: []catalog.Rendition{
			{Format: "avif", Width: 400, Height: 400, URL: "https://img.test/" + slug + "-400.avif"},
			{Format: "webp", Width: 400, Height: 400, URL: "https://img.test/" + slug + "-400.webp"},
			{Format: "webp", Width: 800, Height: 800, URL: "https://img.test/" + slug + "-800.webp"},
		}}},
	}
}

func newFixture(t testing.TB, opts ...func(*web.Deps)) fixture {
	t.Helper()
	cat := catalog.Category{ID: gen.New(), Slug: "tshirts", Name: "تيشيرتات"}
	shirt := product("تيشيرت قطن", "cotton-tee", 15000, true, cat.ID)
	shirt.Options = []catalog.Option{{Name: "المقاس", Values: []string{"M", "L"}}}
	m := egp(20000)
	shirt.Variants = []catalog.StorefrontVariant{
		{ID: gen.New(), OptionValues: []string{"M"}, Price: egp(15000), CompareAt: &m, Available: true},
		{ID: gen.New(), OptionValues: []string{"L"}, Price: egp(16000), Available: false},
	}
	cat2 := product("كاب", "cap", 9900, false)
	fc := &fakeCatalog{products: []catalog.StorefrontProduct{shirt, cat2}, cats: []catalog.Category{cat}, calls: map[string]int{}}
	content := &fakeContent{
		shop: app.ShopContent{Theme: domain.DefaultTheme(), Policies: []domain.Page{{Kind: domain.KindPolicy, Slug: "refund", Status: domain.StatusPublished}}},
		home: domain.DefaultHome(), landings: map[string]domain.Page{},
		policies: map[string]domain.Page{"refund": {ID: gen.New(), Kind: domain.KindPolicy, Slug: "refund", Body: "خلال ١٤ يومًا.\n\nبشرط سلامة المنتج.", Status: domain.StatusPublished}},
	}
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	carts := &fakeCarts{carts: map[authtoken.Token]map[ulid.ID]int{}, price: map[ulid.ID]catalog.StorefrontProduct{}}
	for _, pr := range fc.products {
		for _, v := range pr.Variants {
			carts.price[v.ID] = pr
		}
	}
	deps := web.Deps{
		Catalog: fc, Stores: fakeStores{tenancy.Store{Name: "متجر منى", Locale: "ar", Currency: "EGP"}}, Content: content, Carts: carts,
		Signer: fakeSigner{}, Assets: assets, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPS: true,
		Now: func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) },
	}
	for _, o := range opts {
		o(&deps)
	}
	shop, err := web.New(deps)
	if err != nil {
		t.Fatal(err)
	}
	store := tenant.StoreID{ID: gen.New()}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shop.ServeHTTP(w, r.WithContext(tenant.WithStore(r.Context(), store)))
	})
	return fixture{carts: carts, shop: shop, h: h, catalog: fc, content: content, store: store, shirt: shirt, cat: cat}
}

func (f fixture) get(t testing.TB, path string) *httptest.ResponseRecorder {
	t.Helper()
	rq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	rq.Host = "mona.iftahonline.com"
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	return rec
}

func wantContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}

var (
	styleRE   = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	handlerRE = regexp.MustCompile(`<[a-z][^>]*\son[a-z]+=`)
)

// checkCSP proves the inline stylesheet is exactly what the CSP allows,
// and nothing else is inline.
func checkCSP(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	m := styleRE.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no inline style")
	}
	sum := sha256.Sum256([]byte(m[1]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	policy := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "style-src 'self' "+want) || !strings.Contains(policy, "script-src 'self';") || strings.Contains(policy, "unsafe") {
		t.Fatalf("CSP %q does not allow exactly the inline style %s", policy, want)
	}
	if n := strings.Count(rec.Body.String(), "<script"); n > 2 { // our module + structured data
		t.Fatalf("%d script elements", n)
	}
	if strings.Contains(rec.Body.String(), "<script>") || handlerRE.MatchString(rec.Body.String()) {
		t.Fatal("inline script or event handler in the page")
	}
}

func TestHomeArabicAndEnglish(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	rec := f.get(t, "/")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(strings.ToLower(body), "<!doctype html>") {
		t.Fatalf("status %d", rec.Code)
	}
	wantContains(t, body,
		`<html lang="ar" dir="rtl">`, "<title>متجر منى</title>",
		`<h1>متجر منى</h1>`,                                     // hero heading defaults to the store name
		"وصل حديثًا", "تيشيرت قطن", "150 ج.م", "<s>200 ج.م</s>", // newest grid, price and compare-at
		"نفدت الكمية",                                   // the cap is sold out
		"الدفع عند الاستلام", "شحن سريع", "استرجاع سهل", // default badges in Arabic
		`href="/c/tshirts"`, `href="/p/cotton-tee"`,
		`hreflang="en" href="https://mona.iftahonline.com/en/"`, `rel="canonical" href="https://mona.iftahonline.com/"`,
		"سياسة الاسترجاع", `href="/policies/refund"`,
		`<script type="module" src="/assets/app.`, `rel="preload"`, `fetchpriority="high"`,
	)
	checkCSP(t, rec)
	h := rec.Header()
	if h.Get("Cache-Control") != "public, max-age=0, s-maxage=60" || h.Get("Content-Language") != "ar" {
		t.Fatalf("headers %v", h)
	}
	if tags := h.Get("Cache-Tag"); !strings.HasPrefix(tags, "s-"+f.store.String()) || !strings.Contains(tags, "p-"+f.shirt.ID.String()) ||
		!strings.Contains(tags, "l-"+f.store.String()) {
		t.Fatalf("Cache-Tag %q", tags)
	}

	en := f.get(t, "/en/")
	body = en.Body.String()
	wantContains(t, body, `<html lang="en" dir="ltr">`, "New arrivals", "EGP 150", "Cash on delivery", `href="/en/p/cotton-tee"`,
		`hreflang="ar" href="https://mona.iftahonline.com/"`, "Refund policy", `>العربية</a>`)
	if en.Header().Get("Content-Language") != "en" {
		t.Fatal("English page not marked English")
	}
	if f.get(t, "/en").Code != http.StatusOK {
		t.Fatal("/en without slash")
	}
}

func TestProductPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.get(t, "/p/cotton-tee")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	wantContains(t, body,
		"<title>تيشيرت قطن | متجر منى</title>", `<h1>تيشيرت قطن</h1>`, `href="/c/tshirts"`,
		`<select name="variant"`, "M — 150 ج.م", "L — 160 ج.م (نفدت الكمية)", ` disabled`,
		`data-amount="160 ج.م"`, `action="/cart"`, "قطن مصري ١٠٠٪.", "<p>يغسل في الغسالة.</p>",
		`<meta name="description" content="قطن مصري ١٠٠٪.">`, `property="og:image"`,
	)
	m := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no structured data")
	}
	var ld []map[string]any
	if err := json.Unmarshal([]byte(m[1]), &ld); err != nil || len(ld) != 2 {
		t.Fatalf("structured data %q: %v", m[1], err)
	}
	offers := ld[0]["offers"].(map[string]any)
	if ld[0]["@type"] != "Product" || offers["@type"] != "AggregateOffer" || offers["lowPrice"] != "150.00" ||
		offers["highPrice"] != "160.00" || offers["priceCurrency"] != "EGP" || offers["availability"] != "https://schema.org/InStock" ||
		ld[1]["@type"] != "BreadcrumbList" || len(ld[1]["itemListElement"].([]any)) != 3 {
		t.Fatalf("structured data %v", ld)
	}
	checkCSP(t, rec)

	if rec = f.get(t, "/p/Cotton-Tee"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/p/cotton-tee" {
		t.Fatalf("upper-case slug: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = f.get(t, "/en/p/COTTON-TEE"); rec.Header().Get("Location") != "/en/p/cotton-tee" {
		t.Fatalf("upper-case English slug: %q", rec.Header().Get("Location"))
	}
	rec = f.get(t, "/p/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "الصفحة غير موجودة") || rec.Header().Get("Cache-Tag") != "" {
		t.Fatalf("missing product: %d", rec.Code)
	}
	for _, path := range []string{"/p/", "/p/a/b", "/x", "/pages/", "/c/unknown", "/policies/terms", "/policies/cookies"} {
		if rec = f.get(t, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestCategoryPagination(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var extra []catalog.StorefrontProduct
	for i := range 30 {
		extra = append(extra, product("Tee "+string(rune('A'+i)), "tee-"+string(rune('a'+i)), 10000, true, f.cat.ID))
	}
	f.catalog.products = append(extra, f.catalog.products...)
	body := f.get(t, "/c/tshirts").Body.String()
	if n := strings.Count(body, `class="card"`); n != 24 {
		t.Fatalf("%d cards on the first page", n)
	}
	m := regexp.MustCompile(`href="(/c/tshirts\?after=[0-9A-Z]{26})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no next-page link")
	}
	next := f.get(t, m[1]).Body.String()
	if n := strings.Count(next, `class="card"`); n != 7 || strings.Contains(next, "?after=") {
		t.Fatalf("second page: %d cards", n)
	}
	// A junk cursor is ignored (first page), not an error and not a new cache key.
	if rec := f.get(t, "/c/tshirts?after=junk&utm_source=x"); rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `class="card"`) != 24 {
		t.Fatalf("junk cursor: %d", rec.Code)
	}
}

func TestLandingPageSections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pid := f.shirt.ID.String()
	ends := time.Date(2026, 10, 22, 13, 30, 15, 0, time.UTC).Format(time.RFC3339)
	f.content.landings["summer"] = domain.Page{ID: gen.New(), Kind: domain.KindLanding, Slug: "summer", Title: "عرض الصيف", Status: domain.StatusPublished,
		Sections: []domain.Section{
			{ID: "main", Type: domain.SectionProductLanding, Settings: json.RawMessage(`{"product_id":"` + pid + `","headline":"العرض الأقوى","highlights":["شحن مجاني"]}`)},
			{ID: "gone", Type: domain.SectionProductLanding, Settings: json.RawMessage(`{"product_id":"` + gen.New().String() + `"}`)},
			{ID: "timer", Type: domain.SectionCountdown, Settings: json.RawMessage(`{"ends_at":"` + ends + `"}`)},
			{ID: "ended", Type: domain.SectionCountdown, Settings: json.RawMessage(`{"ends_at":"2020-01-01T00:00:00Z"}`)},
			{ID: "rev", Type: domain.SectionReviews, Settings: json.RawMessage(`{"items":[{"name":"منى","location":"القاهرة","rating":4,"text":"وصل بسرعة <b>جدًا</b>"}]}`)},
			{ID: "faq", Type: domain.SectionFAQ, Settings: json.RawMessage(`{"items":[{"question":"متى يصل؟","answer":"خلال ٣ أيام"}]}`)},
			{ID: "picks", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"manual","product_ids":["` + pid + `"]}`)},
			{ID: "cat", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"category","category_id":"` + f.cat.ID.String() + `","limit":4}`)},
		}}
	rec := f.get(t, "/pages/summer")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	wantContains(t, body,
		"<h1>العرض الأقوى</h1>", "<li>شحن مجاني</li>", "اطلب الآن", // product landing with the default CTA
		`data-countdown="`+ends+`"`, "ينتهي العرض خلال", ">02<", ">01<", ">30<", ">15<", // 2d 1h 30m 15s left at render time
		"★★★★☆", "التقييم 4 من 5", "منى", "· القاهرة", "وصل بسرعة &lt;b&gt;جدًا&lt;/b&gt;",
		"<summary>متى يصل؟</summary>", "تيشيرتات", `href="/c/tshirts"`,
	)
	if strings.Count(body, "data-countdown=") != 1 {
		t.Fatal("an ended countdown with hide_when_ended must not render")
	}
	if c := f.catalog.calls0("byIDs"); c != 1 {
		t.Fatalf("ProductsByIDs called %d times; every picked product must come from one call", c)
	}
	if f.get(t, "/pages/draft").Code != http.StatusNotFound {
		t.Fatal("unpublished landing page served")
	}
	checkCSP(t, rec)
}

func TestPolicyPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/policies/refund").Body.String()
	wantContains(t, body, "<h1>سياسة الاسترجاع</h1>", "<p>خلال ١٤ يومًا.</p>", "<p>بشرط سلامة المنتج.</p>")
	wantContains(t, f.get(t, "/en/policies/refund").Body.String(), "<h1>Refund policy</h1>")
}

func TestMerchantTextIsEscaped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.home = domain.Page{Kind: domain.KindHome, Sections: []domain.Section{
		{ID: "h", Type: domain.SectionHero, Settings: json.RawMessage(`{"heading":"<img src=x onerror=alert(1)>","button_label":"go","button_link":"javascript:alert(1)"}`)},
	}}
	f.catalog.products[0].Title = `"><script>alert(1)</script>`
	f.catalog.products[0].Description = "</script><script>alert(2)</script>"
	home := f.get(t, "/").Body.String()
	if strings.Contains(home, "<img src=x") || strings.Contains(home, "javascript:") {
		t.Fatalf("hero text or link not neutralized: %s", home)
	}
	wantContains(t, home, "&lt;img src=x onerror=alert(1)&gt;", `href="#"`)
	prod := f.get(t, "/p/cotton-tee")
	if strings.Contains(prod.Body.String(), "<script>alert") {
		t.Fatal("product text reached the page unescaped")
	}
	checkCSP(t, prod)
}

func TestCacheAndInvalidate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := f.get(t, "/").Body.String()
	lists, views := f.catalog.calls0("list"), f.catalog.calls0("categories")
	if f.get(t, "/").Body.String() != first || f.catalog.calls0("list") != lists || f.catalog.calls0("categories") != views {
		t.Fatal("a repeated page was not served from the cache")
	}
	f.get(t, "/en/")
	if f.catalog.calls0("categories") != views {
		t.Fatal("the store view must be shared by every page and language")
	}
	f.catalog.mu.Lock()
	f.catalog.products[0].Variants[0].Price = egp(12500)
	f.catalog.mu.Unlock()
	if !strings.Contains(f.get(t, "/").Body.String(), "150 ج.م") {
		t.Fatal("the cached page should still show the old price until invalidated")
	}
	f.shop.Invalidate(f.store)
	if !strings.Contains(f.get(t, "/").Body.String(), "125 ج.م") || f.catalog.calls0("categories") != views+1 {
		t.Fatal("after Invalidate the page must be rendered again from fresh data")
	}
	f.shop.Invalidate(tenant.StoreID{ID: gen.New()}) // another store: no effect
	if f.catalog.calls0("categories") != views+1 {
		t.Fatal("invalidating another store re-rendered this one")
	}
}

func TestMethodsAndFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/p/cotton-tee", nil)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST to a page: %d", rec.Code)
	}
	rq = httptest.NewRequestWithContext(context.Background(), http.MethodHead, "/", nil)
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d with %d bytes", rec.Code, rec.Body.Len())
	}
	f.content.err = errors.New("db down")
	f.shop.Invalidate(f.store)
	if rec = f.get(t, "/"); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("failure: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	f.shop.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)) // no tenant
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no tenant: %d", rec.Code)
	}
}

func TestAssets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`src="(/assets/app\.[0-9a-f]{8}\.js)"`).FindStringSubmatch(f.get(t, "/").Body.String())
	if m == nil {
		t.Fatal("no script URL")
	}
	for path, want := range map[string]int{m[1]: http.StatusOK, "/assets/app.js": http.StatusNotFound, "/assets/../go.mod": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
		if want == http.StatusOK && (rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || rec.Body.Len() == 0 || rec.Body.Len() > 30<<10) {
			t.Errorf("%s: headers %v, %d bytes", path, rec.Header(), rec.Body.Len())
		}
	}
	font := regexp.MustCompile(`url\((/assets/cairo-arabic-400\.[0-9a-f]{8}\.woff2)\)`).FindStringSubmatch(f.get(t, "/").Body.String())
	if font == nil {
		t.Fatal("no font face for the default font")
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, font[1], nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "font/woff2" || !strings.HasPrefix(rec.Body.String(), "wOF2") {
		t.Fatalf("font: %d %v", rec.Code, rec.Header())
	}
}

func TestSystemFontDownloadsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.shop.Theme.Font = domain.FontSystem
	body := f.get(t, "/").Body.String()
	if strings.Contains(body, "@font-face") || strings.Contains(body, `rel="preload"`) {
		t.Fatal("the system font must not download or preload anything")
	}
}

func TestEdgeTTLAndTags(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(d *web.Deps) { d.EdgeTTL = 10 * time.Minute })
	rec := f.get(t, "/p/cotton-tee")
	if rec.Header().Get("Cache-Control") != "public, max-age=0, s-maxage=600" {
		t.Fatalf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	// A product page is not a listing: a new product elsewhere does not
	// purge it.
	if tags := rec.Header().Get("Cache-Tag"); strings.Contains(tags, "l-") || !strings.Contains(tags, "p-"+f.shirt.ID.String()) {
		t.Fatalf("product page tags %q", tags)
	}
	if tags := f.get(t, "/c/tshirts").Header().Get("Cache-Tag"); !strings.Contains(tags, "c-"+f.cat.ID.String()) || !strings.Contains(tags, "l-") {
		t.Fatalf("category page tags %q", tags)
	}
}

// TestCompression: pages go out gzipped to clients that accept it (the
// same bytes once inflated), plain to the others, with Vary so caches
// keep the two apart; private pages too.
func TestCompression(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	get := func(path, enc string) *httptest.ResponseRecorder {
		rq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		rq.Host = "mona.iftahonline.com"
		if enc != "" {
			rq.Header.Set("Accept-Encoding", enc)
		}
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, rq)
		return rec
	}
	for _, path := range []string{"/p/cotton-tee", "/cart"} {
		plain := get(path, "")
		zipped := get(path, "br, gzip;q=0.8")
		if plain.Header().Get("Content-Encoding") != "" || zipped.Header().Get("Content-Encoding") != "gzip" ||
			!strings.Contains(zipped.Header().Get("Vary"), "Accept-Encoding") || !strings.Contains(plain.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatalf("%s headers: plain %v, zipped %v", path, plain.Header(), zipped.Header())
		}
		size := zipped.Body.Len()
		zr, err := gzip.NewReader(zipped.Body)
		if err != nil {
			t.Fatal(err)
		}
		inflated, err := io.ReadAll(zr)
		if err != nil || string(inflated) != plain.Body.String() {
			t.Fatalf("%s: inflated body differs (%v)", path, err)
		}
		if size >= plain.Body.Len()/2 || zipped.Header().Get("Content-Length") != strconv.Itoa(size) {
			t.Fatalf("%s: %d bytes gzipped of %d", path, size, plain.Body.Len())
		}
	}
	if rec := get("/p/cotton-tee", "gzip;q=0"); rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 refuses gzip")
	}
}
