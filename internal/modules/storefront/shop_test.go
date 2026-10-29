package storefront_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// platform is one process with every module the storefront touches,
// wired as cmd/platform wires them: the merchant API (catalog and
// storefront, behind a fake membership check), the shop behind host
// resolution, and a worker delivering events.
type platform struct {
	api  http.Handler // merchant API
	shop http.Handler // storefront
	ten  *tenancy.Module
	pool *pgxpool.Pool
}

func newPlatform(t *testing.T) (platform, map[string]testkit.FakeMember) {
	t.Helper()
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()

	bus := eventbus.New(ins)
	for _, typ := range storefront.InvalidatingEvents() {
		if err = bus.Subscribe(typ, storefront.InvalidateSubscriber, storefront.InvalidateHandler(rdb)); err != nil {
			t.Fatal(err)
		}
	}
	if err = bus.Subscribe(shipping.EventRatesChanged, shipping.InvalidateSubscriber, shipping.InvalidateHandler(rdb)); err != nil {
		t.Fatal(err)
	}
	purchases := &laterHandler{}
	if err = bus.Subscribe(orders.EventOrderPlaced, marketing.PurchasesSubscriber, purchases.handle); err != nil {
		t.Fatal(err)
	}

	ten, err := tenancy.New(tenancy.Deps{
		Pool: pool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, DNS: &net.Resolver{PreferGo: true},
		Config: config.Tenancy{BaseDomain: "iftahonline.com", HostCacheSize: 100, HostCacheTTL: time.Minute,
			NegativeCacheSize: 100, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	media := config.Media{ // nothing here is contacted: no images in this test
		S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "bucket",
		ImgproxyURL: "https://img.test", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
		MaxUploadBytes: 1 << 20, MaxImagePixels: 1 << 20,
	}
	stores := ten.Service()
	cat, err := catalog.New(catalog.Deps{
		Pool: pool, Jobs: ins, IDs: ids, Log: log, Keyring: keyring, Events: bus, Media: media,
		Search: config.Search{URL: "http://127.0.0.1:1", Index: "products"},
		Currency: catalog.CurrencyFunc(func(ctx context.Context) (money.Currency, error) {
			st, getErr := stores.GetStore(ctx)
			return st.Currency, getErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	look, err := storefront.New(storefront.Deps{Pool: pool, IDs: ids, Events: bus, Jobs: ins, Media: media})
	if err != nil {
		t.Fatal(err)
	}
	products, err := catalog.NewStorefront(catalog.StorefrontDeps{Pool: pool, Media: media})
	if err != nil {
		t.Fatal(err)
	}
	region := func(ctx context.Context) (geo.Country, error) {
		st, getErr := stores.GetStore(ctx)
		country, _ := geo.CountryOf(st.Currency)
		return country, getErr
	}
	cust, err := customers.New(customers.Deps{Pool: pool, IDs: ids, Region: func(ctx context.Context) (phone.Region, error) {
		c, regionErr := region(ctx)
		return phone.Region(c), regionErr
	}})
	if err != nil {
		t.Fatal(err)
	}
	ship, err := shipping.New(shipping.Deps{Pool: pool, IDs: ids, Redis: rdb, Log: log, Events: bus,
		Stores: func(ctx context.Context) (shipping.StoreInfo, error) {
			st, getErr := stores.GetStore(ctx)
			c, _ := geo.CountryOf(st.Currency)
			return shipping.StoreInfo{Currency: st.Currency, Country: c}, getErr
		}})
	if err != nil {
		t.Fatal(err)
	}
	orderDeps := orders.ShopDeps{Pool: pool, IDs: ids, Events: bus, Catalog: cat.Catalog(), Customers: cust.Customers(),
		Shipping: ship.Quoter(),
		Stores: func(ctx context.Context) (orders.StoreInfo, error) {
			st, getErr := stores.GetStore(ctx)
			c, _ := geo.CountryOf(st.Currency)
			return orders.StoreInfo{Currency: st.Currency, Country: c, Location: time.UTC}, getErr
		}}
	shopOrders, err := orders.NewShop(orderDeps)
	if err != nil {
		t.Fatal(err)
	}
	convOrders, err := orders.NewConversions(orderDeps)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(keyring))
	if err != nil {
		t.Fatal(err)
	}
	// One module for every role here: the api's pixels, the storefront's
	// conversions and the worker's sends, to the stand-in Meta.
	mkt, err := marketing.New(marketing.Deps{Pool: pool, IDs: ids, Sealer: sealer, Events: bus, Jobs: ins, Orders: convOrders,
		Endpoints: marketing.Endpoints{Meta: metaSink().URL}})
	if err != nil {
		t.Fatal(err)
	}
	purchases.set(mkt.OrderPlacedHandler())
	startWorker(t, pool, bus, log, mkt.AddWorkers)
	shop, err := storefront.NewShop(storefront.ShopDeps{Pool: pool, Redis: rdb, Catalog: products, Stores: stores, Media: media, Log: log,
		Cache: storefront.DefaultCacheConfig(), HTTPS: true, Orders: shopOrders, Pixels: mkt.Pixels(), Conversions: mkt.Conversions(),
		ClientIP: func(r *http.Request) string {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			return host
		}})
	if err != nil {
		t.Fatal(err)
	}
	listenCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	t.Cleanup(stop)
	go shop.Listen(listenCtx)
	go ship.Listen(listenCtx)

	resp := problem.NewResponder(log)
	members := map[string]testkit.FakeMember{}
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{authz.RoleOwner: authz.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	apiMux := http.NewServeMux()
	scope := testkit.FakeStoreScope(resp, members)
	cat.Mount(apiMux, pol, scope, resp)
	look.Mount(apiMux, pol, scope, resp)
	cust.Mount(apiMux, pol, scope, resp)
	ship.Mount(apiMux, pol, scope, resp)
	mkt.Mount(apiMux, pol, scope, resp)
	shopMux := http.NewServeMux()
	server.MountStorefront(ten.Resolver(), shop.Pages(), storefront.AssetsPrefix, shop.Assets())(server.Routes{Mux: shopMux, Problems: resp, Log: log})
	return platform{api: apiMux, shop: shopMux, ten: ten, pool: pool}, members
}

// startWorker runs event deliveries, and the jobs add registers, as the
// worker role does.
func startWorker(t *testing.T, pool *pgxpool.Pool, bus *eventbus.Bus, log *slog.Logger, add ...func(*river.Workers) error) {
	t.Helper()
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	for _, a := range add {
		if err := a(workers); err != nil {
			t.Fatal(err)
		}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: log, Workers: workers, Middleware: jobs.Middleware(), TestOnly: true,
		// Only event deliveries and the conversions are registered: the
		// catalog's search jobs are not this test's business and wait.
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithoutCancel(t.Context())
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})
}

type storeRef struct {
	id    tenant.StoreID
	host  string
	token string
}

func (p platform) createStore(t *testing.T, members map[string]testkit.FakeMember, slug, name, locale, currency string) storeRef {
	t.Helper()
	id := tenant.StoreID{ID: ulid.NewGenerator().New()}
	ctx := tenant.WithStore(t.Context(), id)
	err := postgres.InTenantTx(ctx, p.pool, func(tx pgx.Tx) error {
		_, err := p.ten.Service().CreateStoreTx(ctx, tx, tenancy.NewStore{Slug: slug, Name: name, Currency: currency, Locale: locale, Timezone: "Africa/Cairo"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "owner-" + slug
	members[token] = testkit.FakeMember{UserID: ulid.NewGenerator().New(), Roles: map[tenant.StoreID]authz.Role{id: authz.RoleOwner}}
	return storeRef{id: id, host: slug + ".iftahonline.com", token: token}
}

// call sends a merchant API request and decodes the JSON answer.
func (p platform) call(t *testing.T, s storeRef, method, path, body string, want int) map[string]any {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), method, "/v1/stores/"+s.id.String()+path, strings.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+s.token)
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.api.ServeHTTP(rec, rq)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// visit is a shopper's GET on a store host.
func (p platform) visit(t *testing.T, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	rq.Host = host
	rec := httptest.NewRecorder()
	p.shop.ServeHTTP(rec, rq)
	return rec
}

// TestTwoStoresTwoHosts is the P3 acceptance test: one process renders
// two stores on their own hosts, each with its own theme, language and
// products, and nothing of one shows on the other.
func TestTwoStoresTwoHosts(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	a := p.createStore(t, members, "mona-"+tag, "متجر منى", "ar", "EGP")
	b := p.createStore(t, members, "dubai-"+tag, "Dubai Goods", "en", "AED")

	p.call(t, a, http.MethodPut, "/storefront/theme", `{"version":0,"settings":{"colors":{"primary":"#0f766e","accent":"#f59e0b","background":"#ffffff","text":"#1f2937"},"font":"cairo","radius":"medium"}}`, http.StatusOK)
	p.call(t, b, http.MethodPut, "/storefront/theme", `{"version":0,"settings":{"colors":{"primary":"#7c3aed","accent":"#eab308","background":"#0b1120","text":"#f8fafc"},"font":"tajawal","radius":"large"}}`, http.StatusOK)
	p.call(t, b, http.MethodPut, "/storefront/home", `{"title":"Dubai Goods – Home","sections":[{"type":"hero","settings":{"heading":"Welcome to Dubai Goods"}},{"type":"product_grid","settings":{"source":"newest"}}]}`, http.StatusOK)
	p.call(t, a, http.MethodPost, "/products", `{"title":"قميص كتان","slug":"linen-shirt","status":"active","variants":[{"price_minor":15000,"on_hand":5}]}`, http.StatusCreated)
	p.call(t, b, http.MethodPost, "/products", `{"title":"Oud Perfume","slug":"oud","status":"active","variants":[{"price_minor":24900,"on_hand":5}]}`, http.StatusCreated)

	ra, rb := p.visit(t, a.host, "/"), p.visit(t, b.host, "/")
	if ra.Code != http.StatusOK || rb.Code != http.StatusOK {
		t.Fatalf("status A %d, B %d", ra.Code, rb.Code)
	}
	bodyA, bodyB := ra.Body.String(), rb.Body.String()
	for _, want := range []string{`lang="ar" dir="rtl"`, "--c-primary:#0f766e", `font-family:"Cairo"`, "قميص كتان", "150 ج.م", "<h1>متجر منى</h1>"} {
		if !strings.Contains(bodyA, want) {
			t.Errorf("store A lacks %q", want)
		}
	}
	for _, want := range []string{`lang="en" dir="ltr"`, "--c-primary:#7c3aed", `font-family:"Tajawal"`, "Oud Perfume", "AED 249", "Welcome to Dubai Goods", "<title>Dubai Goods – Home</title>"} {
		if !strings.Contains(bodyB, want) {
			t.Errorf("store B lacks %q", want)
		}
	}
	// The base stylesheet is the same for every store (it may hold any
	// color, e.g. a swatch); a store's own colors are its theme variables.
	for _, leak := range []string{"Oud Perfume", "--c-primary:#7c3aed", "--c-accent:#eab308", "Dubai Goods"} {
		if strings.Contains(bodyA, leak) {
			t.Errorf("store A shows B's %q", leak)
		}
	}
	for _, leak := range []string{"قميص كتان", "--c-primary:#0f766e", "--c-accent:#f59e0b", "متجر منى"} {
		if strings.Contains(bodyB, leak) {
			t.Errorf("store B shows A's %q", leak)
		}
	}
	// A's product is not reachable on B's host, and an unknown host is
	// the platform's 404.
	if rec := p.visit(t, b.host, "/p/linen-shirt"); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "قميص") {
		t.Fatalf("A's product on B's host: %d", rec.Code)
	}
	if rec := p.visit(t, "nobody-"+tag+".iftahonline.com", "/"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Store not found") {
		t.Fatalf("unknown host: %d", rec.Code)
	}
	// The second language is under its prefix on each store.
	if rec := p.visit(t, a.host, "/en/p/linen-shirt"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "EGP 150") {
		t.Fatalf("A in English: %d", rec.Code)
	}
	if rec := p.visit(t, b.host, "/ar/p/oud"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "249 د.إ") {
		t.Fatalf("B in Arabic: %d", rec.Code)
	}
}

// TestPriceEditReachesStorefront is the P3 acceptance test for
// freshness: a price edited in the dashboard shows on the cached product
// page well within 60 s. The path is the production one: the catalog
// publishes catalog.product.changed in the edit's transaction, the
// worker delivers it, the handler publishes on Redis, and the shop drops
// the store's cached pages.
func TestPriceEditReachesStorefront(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	s := p.createStore(t, members, "fresh-"+tag, "متجر الطازج", "ar", "EGP")
	prod := p.call(t, s, http.MethodPost, "/products", `{"title":"عسل","slug":"honey","status":"active","variants":[{"price_minor":30000,"on_hand":5}]}`, http.StatusCreated)
	variant := prod["variants"].([]any)[0].(map[string]any)["id"].(string)

	if body := p.visit(t, s.host, "/p/honey").Body.String(); !strings.Contains(body, "300 ج.م") {
		t.Fatal("first render lacks the price")
	}
	if body := p.visit(t, s.host, "/").Body.String(); !strings.Contains(body, "300 ج.م") {
		t.Fatal("home lacks the price")
	}
	// Both pages are now cached; the cache's TTL (30 s) is longer than
	// the wait below, so only the event path can refresh them in time.
	edited := time.Now()
	p.call(t, s, http.MethodPut, "/products/"+prod["id"].(string),
		fmt.Sprintf(`{"title":"عسل","slug":"honey","status":"active","variants":[{"id":%q,"price_minor":27500}]}`, variant), http.StatusOK)

	// Both pages must show the new price within the deadline. They are
	// checked together: an earlier event (the product's creation, late
	// under load) can drop the product page alone before the edit's event
	// arrives, so the product page refreshing first proves nothing about
	// the home page.
	deadline := edited.Add(15 * time.Second)
	pages := map[string]bool{"/p/honey": false, "/": false}
	for {
		fresh := true
		for path, done := range pages {
			if !done {
				pages[path] = strings.Contains(p.visit(t, s.host, path).Body.String(), "275 ج.م")
			}
			fresh = fresh && pages[path]
		}
		if fresh {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new price did not reach the cached pages through the event path: %v", pages)
		}
		time.Sleep(50 * time.Millisecond)
	}
	took := time.Since(edited)
	t.Logf("price edit visible on the storefront after %v (limit 60s)", took.Round(time.Millisecond))
}

// TestOrderFromLandingPage: a shopper orders from a landing page's
// one-step form on the store's host; the real orders, customers and
// catalog modules place it, the thank-you page shows it, and a repeat of
// the same submit (the same idempotency key) lands on the same order. A
// blocked phone reads only a generic refusal.
func TestOrderFromLandingPage(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	s := p.createStore(t, members, "cod-"+tag, "متجر الدفع عند الاستلام", "ar", "EGP")
	prod := p.call(t, s, http.MethodPost, "/products", `{"title":"ساعة","slug":"watch","status":"active","variants":[{"price_minor":45000,"on_hand":3}]}`, http.StatusCreated)
	variant := prod["variants"].([]any)[0].(map[string]any)["id"].(string)
	p.call(t, s, http.MethodPost, "/storefront/pages", `{"title":"عرض الساعة","slug":"watch-offer","status":"published","sections":[{"type":"product_landing","settings":{"product_id":"`+prod["id"].(string)+`"}}]}`, http.StatusCreated)

	page := p.visit(t, s.host, "/pages/watch-offer").Body.String()
	if !strings.Contains(page, `action="/order"`) || !strings.Contains(page, `<option value="EG-C">القاهرة</option>`) {
		t.Fatal("the landing page lacks the one-step form")
	}
	form := url.Values{"variant": {variant}, "qty": {"2"}, "key": {"landing-submit-1"}, "name": {"منى"}, "phone": {"01012345678"},
		"governorate": {"EG-C"}, "city": {"مدينة نصر"}, "address": {"12 شارع عباس العقاد"}}
	shopper := 0
	order := func(form url.Values) *httptest.ResponseRecorder { // each call is another shopper's phone and network
		shopper++
		rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/order", strings.NewReader(form.Encode()))
		rq.Host = s.host
		rq.RemoteAddr = fmt.Sprintf("198.51.100.%d:40000", shopper)
		rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rq.Header.Set("Origin", "https://"+s.host)
		rec := httptest.NewRecorder()
		p.shop.ServeHTTP(rec, rq)
		return rec
	}
	rec := order(form)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/thank-you/") {
		t.Fatalf("order: %d %s", rec.Code, rec.Body)
	}
	thanks := p.visit(t, s.host, loc)
	if body := thanks.Body.String(); thanks.Code != http.StatusOK || !strings.Contains(body, "ساعة × 2") || !strings.Contains(body, "900 ج.م") ||
		!strings.Contains(body, strings.TrimPrefix(loc, "/thank-you/")[18:]) {
		t.Fatalf("thank-you: %d %s", thanks.Code, body)
	}
	if again := order(form); again.Header().Get("Location") != loc {
		t.Fatalf("the repeated submit made another order: %q vs %q", again.Header().Get("Location"), loc)
	}
	// Stock is held: only one unit is left for the next shopper.
	more := url.Values{}
	for k, v := range form {
		more[k] = v
	}
	more.Set("key", "landing-submit-2")
	more.Set("phone", "01099999999")
	if rec = order(more); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "الكمية المطلوبة غير متوفرة.") {
		t.Fatalf("short stock: %d", rec.Code)
	}

	p.call(t, s, http.MethodPost, "/customers/blocks", `{"phone":"01055555555"}`, http.StatusOK)
	blocked := url.Values{}
	for k, v := range more {
		blocked[k] = v
	}
	blocked.Set("qty", "1")
	blocked.Set("key", "landing-submit-3")
	blocked.Set("phone", "01055555555")
	rec = order(blocked)
	if body := rec.Body.String(); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "تعذّر إتمام الطلب. تواصل مع المتجر.") ||
		strings.Contains(body, "blocked") || strings.Contains(body, "محظور") {
		t.Fatalf("blocked: %d %s", rec.Code, body)
	}
	// Another store's host does not know the order.
	other := p.createStore(t, members, "other-"+tag, "متجر آخر", "ar", "EGP")
	if rec = p.visit(t, other.host, loc); rec.Code != http.StatusNotFound {
		t.Fatalf("thank-you on another store: %d", rec.Code)
	}
}

// TestOrderWithShipping: the store's zones price the order. The form's
// quote shows shipping, the COD fee and the total before submitting; a
// governorate outside the zones is refused with a clear message; the
// order and its thank-you page carry the same amounts.
func TestOrderWithShipping(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	s := p.createStore(t, members, "ship-"+tag, "متجر الشحن", "ar", "EGP")
	prod := p.call(t, s, http.MethodPost, "/products", `{"title":"ساعة","slug":"watch","status":"active","variants":[{"price_minor":45000,"on_hand":5}]}`, http.StatusCreated)
	variant := prod["variants"].([]any)[0].(map[string]any)["id"].(string)
	p.call(t, s, http.MethodPost, "/shipping/zones", `{"name":"القاهرة","areas":["EG-C"],"rate_minor":5000,"free_over_minor":200000,"eta_min_days":2,"eta_max_days":3,"cities":[{"name":"مدينة نصر","rate_minor":4000}]}`, http.StatusCreated)
	p.call(t, s, http.MethodPut, "/shipping/settings", `{"version":0,"cod_fee":{"fixed_minor":1000,"bp":0,"min_minor":0,"max_minor":0}}`, http.StatusOK)

	post := func(path string, form url.Values, n int) *httptest.ResponseRecorder {
		rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
		rq.Host = s.host
		rq.RemoteAddr = fmt.Sprintf("203.0.113.%d:40000", n)
		rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rq.Header.Set("Origin", "https://"+s.host)
		rec := httptest.NewRecorder()
		p.shop.ServeHTTP(rec, rq)
		return rec
	}
	quote := func(gov, city string) map[string]any {
		rec := post("/order/quote", url.Values{"variant": {variant}, "qty": {"2"}, "governorate": {gov}, "city": {city}}, 1)
		var q map[string]any
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") || json.Unmarshal(rec.Body.Bytes(), &q) != nil {
			t.Fatalf("quote %s/%s: %d %q %s", gov, city, rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
		}
		return q
	}
	if q := quote("EG-C", "مدينة نصر"); q["deliverable"] != true || q["text"] != "الشحن: 40 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 950 ج.م" {
		t.Fatalf("quote with a city rate: %v", q)
	}
	if q := quote("EG-C", "الزمالك"); q["text"] != "الشحن: 50 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 960 ج.م" {
		t.Fatalf("quote with the zone rate: %v", q)
	}
	// The product page's lines: the zone's days and threshold came from
	// shipping through orders.
	if q := quote("EG-C", ""); q["delivery"] != "التوصيل إلى القاهرة خلال 2–3 أيام" || q["shipping"] != "الشحن 50 ج.م · مجاني فوق 2,000 ج.م" {
		t.Fatalf("delivery lines: %v", q)
	}
	if q := quote("EG-ALX", ""); q["deliverable"] != false || q["text"] != "لا نوصّل إلى هذه المحافظة حاليًا." {
		t.Fatalf("quote outside the zones: %v", q)
	}
	if q := quote("", ""); q["text"] != "" {
		t.Fatalf("quote without a governorate: %v", q)
	}

	form := url.Values{"variant": {variant}, "qty": {"2"}, "key": {"ship-1"}, "name": {"منى أحمد"}, "phone": {"01012345678"},
		"governorate": {"EG-ALX"}, "city": {"سموحة"}, "address": {"14 شارع فوزي معاذ"}}
	if rec := post("/order", form, 2); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "لا نوصّل إلى هذه المحافظة حاليًا.") {
		t.Fatalf("order outside the zones: %d", rec.Code)
	}
	form.Set("governorate", "EG-C")
	form.Set("city", "مدينة نصر")
	form.Set("key", "ship-2")
	rec := post("/order", form, 3)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/thank-you/") {
		t.Fatalf("order: %d %s", rec.Code, rec.Body)
	}
	body := p.visit(t, s.host, loc).Body.String()
	for _, want := range []string{"<span>المجموع</span><span>900 ج.م</span>", "<span>الشحن</span><span>40 ج.م</span>",
		"<span>رسوم الدفع عند الاستلام</span><span>10 ج.م</span>", "<span>الإجمالي</span><strong>950 ج.م</strong>",
		// the first name only, the city and governorate, the zone's days
		"شكراً يا منى، طلبك وصلنا", "التوصيل إلى: مدينة نصر، القاهرة", "خلال 2–3 أيام · مدينة نصر، القاهرة"} {
		if !strings.Contains(body, want) {
			t.Fatalf("thank-you lacks %q", want)
		}
	}
	for _, private := range []string{"أحمد", "فوزي معاذ", "01012345678"} {
		if strings.Contains(body, private) {
			t.Fatalf("thank-you shows %q: its link may be shared", private)
		}
	}

	// Tracking finds it by its code and phone (typed another way), and
	// only on this store's host.
	ref := strings.TrimPrefix(loc, "/thank-you/")
	code := ref[len(ref)-8:]
	track := post("/track", url.Values{"order": {strings.ToLower(code)}, "phone": {"010 1234 5678"}}, 4)
	if tb := track.Body.String(); track.Code != http.StatusOK || !strings.Contains(tb, "#"+code) || !strings.Contains(tb, "تم استلام الطلب") {
		t.Fatalf("track: %d", track.Code)
	}
	other := p.createStore(t, members, "track-"+tag, "متجر آخر", "ar", "EGP")
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/track", strings.NewReader(url.Values{"order": {code}, "phone": {"01012345678"}}.Encode()))
	rq.Host, rq.RemoteAddr = other.host, "203.0.113.5:40000"
	rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rq.Header.Set("Origin", "https://"+other.host)
	crossRec := httptest.NewRecorder()
	p.shop.ServeHTTP(crossRec, rq)
	if crossRec.Code != http.StatusNotFound || strings.Contains(crossRec.Body.String(), "طلب رقم") { // the form shows the typed code back; no order card
		t.Fatalf("another store tracks the order: %d", crossRec.Code)
	}

	// The merchant changes the rate: the next quote follows at once.
	cfg := p.call(t, s, http.MethodGet, "/shipping", "", http.StatusOK)
	zone := cfg["zones"].([]any)[0].(map[string]any)
	p.call(t, s, http.MethodPut, "/shipping/zones/"+zone["id"].(string), `{"version":1,"name":"القاهرة","areas":["EG-C"],"rate_minor":3000}`, http.StatusOK)
	if q := quote("EG-C", "مدينة نصر"); q["text"] != "الشحن: 30 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 940 ج.م" {
		t.Fatalf("quote after the rate change: %v", q)
	}
}
