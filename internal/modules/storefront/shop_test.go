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
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
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
	startWorker(t, pool, bus, log)

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
	shop, err := storefront.NewShop(storefront.ShopDeps{Pool: pool, Redis: rdb, Catalog: products, Stores: stores, Media: media, Log: log,
		Cache: storefront.DefaultCacheConfig(), HTTPS: true})
	if err != nil {
		t.Fatal(err)
	}
	listenCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	t.Cleanup(stop)
	go shop.Listen(listenCtx)

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
	shopMux := http.NewServeMux()
	server.MountStorefront(ten.Resolver(), shop.Pages(), storefront.AssetsPrefix, shop.Assets())(server.Routes{Mux: shopMux, Problems: resp, Log: log})
	return platform{api: apiMux, shop: shopMux, ten: ten, pool: pool}, members
}

// startWorker runs event deliveries as the worker role does.
func startWorker(t *testing.T, pool *pgxpool.Pool, bus *eventbus.Bus, log *slog.Logger) {
	t.Helper()
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: log, Workers: workers, Middleware: jobs.Middleware(), TestOnly: true,
		// Only event deliveries are registered: the catalog's search jobs
		// are not this test's business and simply wait.
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
	for _, leak := range []string{"Oud Perfume", "#7c3aed", "Dubai Goods"} {
		if strings.Contains(bodyA, leak) {
			t.Errorf("store A shows B's %q", leak)
		}
	}
	for _, leak := range []string{"قميص كتان", "#0f766e", "متجر منى"} {
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

	deadline := edited.Add(15 * time.Second)
	for {
		body := p.visit(t, s.host, "/p/honey").Body.String()
		if strings.Contains(body, "275 ج.م") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the new price did not reach the cached product page through the event path")
		}
		time.Sleep(50 * time.Millisecond)
	}
	took := time.Since(edited)
	if !strings.Contains(p.visit(t, s.host, "/").Body.String(), "275 ج.م") {
		t.Fatal("the home page's grid still shows the old price")
	}
	t.Logf("price edit visible on the storefront after %v (limit 60s)", took.Round(time.Millisecond))
}
