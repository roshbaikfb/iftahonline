// Command sfdemo serves a seeded demo store for browser tests (Playwright,
// Lighthouse; ADR-0019). It starts throwaway PostgreSQL and Redis
// containers (like the Go tests), wires the real modules, fills one store
// through their merchant APIs, and serves it with the production
// middleware at http://souq.localhost:<port>/ (browsers resolve
// *.localhost to the loopback address). It is a test tool: never deploy
// it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/otel"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18190", "listen address")
	load := flag.Bool("load", false, "load-test mode: seed a product with deep stock (/p/load-test) and trust the loopback proxy's X-Forwarded-For")
	flag.Parse()
	if err := run(*addr, *load); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "sfdemo:", err)
		os.Exit(1)
	}
}

func run(addr string, load bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	infra, teardown, err := testinfra.Start(startCtx)
	if err != nil {
		return err
	}
	defer teardown()

	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(opts)
	defer func() {
		if cerr := rdb.Close(); cerr != nil {
			log.Warn("redis close", "err", cerr)
		}
	}()

	shop, api, ten, err := wire(pool, rdb, log)
	if err != nil {
		return err
	}
	if err = seed(ctx, pool, ten, api, load); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	tel, err := otel.Setup(ctx, otel.Options{})
	if err != nil {
		return err
	}
	httpCfg := config.HTTP{Addr: addr, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: time.Minute, RequestTimeout: 10 * time.Second, MaxBodyBytes: 1 << 20, ShutdownTimeout: 5 * time.Second}
	if load {
		// k6 on this machine plays the edge proxy: each request names its
		// shopper's address, so per-IP limits apply per shopper as they do
		// behind Cloudflare in production.
		httpCfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	}
	handler := server.NewHandler(server.Deps{
		Log: log, IDs: ulid.NewGenerator(), Telemetry: tel, HTTP: httpCfg, Profile: middleware.ProfileStorefront,
		Health: server.NewHealth(log, server.BuildInfo{Version: "demo", Role: "storefront"}),
		Mount:  server.MountStorefront(ten.Resolver(), shop.Pages(), storefront.AssetsPrefix, shop.Assets()),
	})
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		if serr := srv.Shutdown(shutdown); serr != nil {
			log.Warn("shutdown", "err", serr)
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return err
	}
	fmt.Printf("sfdemo: ready at http://souq.localhost:%s/\n", port) //nolint:forbidigo // the ready line browser tests wait for
	if err = srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// demo is the merchant API with one owner, for seeding.
type demo struct {
	h       http.Handler
	members map[string]testkit.FakeMember
}

func wire(pool *pgxpool.Pool, rdb *redis.Client, log *slog.Logger) (*storefront.Shop, demo, *tenancy.Module, error) {
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		return nil, demo{}, nil, err
	}
	ids := ulid.NewGenerator()
	bus := eventbus.New(ins) // nothing subscribes: pages render fresh
	ten, err := tenancy.New(tenancy.Deps{
		Pool: pool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, DNS: &net.Resolver{PreferGo: true},
		Config: config.Tenancy{BaseDomain: "localhost", HostCacheSize: 100, HostCacheTTL: time.Minute,
			NegativeCacheSize: 100, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute},
	})
	if err != nil {
		return nil, demo{}, nil, err
	}
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		return nil, demo{}, nil, err
	}
	media := config.Media{ // the demo has no images: nothing here is contacted
		S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "demo",
		ImgproxyURL: "http://127.0.0.1:1", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
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
		return nil, demo{}, nil, err
	}
	look, err := storefront.New(storefront.Deps{Pool: pool, IDs: ids, Events: bus, Jobs: ins, Media: media})
	if err != nil {
		return nil, demo{}, nil, err
	}
	products, err := catalog.NewStorefront(catalog.StorefrontDeps{Pool: pool, Media: media})
	if err != nil {
		return nil, demo{}, nil, err
	}
	country := func(ctx context.Context) (geo.Country, error) {
		st, getErr := stores.GetStore(ctx)
		c, _ := geo.CountryOf(st.Currency)
		return c, getErr
	}
	cust, err := customers.New(customers.Deps{Pool: pool, IDs: ids, Region: func(ctx context.Context) (phone.Region, error) {
		c, regionErr := country(ctx)
		return phone.Region(c), regionErr
	}})
	if err != nil {
		return nil, demo{}, nil, err
	}
	ship, err := shipping.New(shipping.Deps{Pool: pool, IDs: ids, Redis: rdb, Log: log, Events: bus,
		Stores: func(ctx context.Context) (shipping.StoreInfo, error) {
			c, infoErr := country(ctx)
			return shipping.StoreInfo{Currency: money.EGP, Country: c}, infoErr
		}})
	if err != nil {
		return nil, demo{}, nil, err
	}
	shopOrders, err := orders.NewShop(orders.ShopDeps{Pool: pool, IDs: ids, Events: bus, Catalog: cat.Catalog(), Customers: cust.Customers(),
		Shipping: ship.Quoter(),
		Stores: func(ctx context.Context) (orders.StoreInfo, error) {
			c, infoErr := country(ctx)
			return orders.StoreInfo{Currency: money.EGP, Country: c, Location: time.UTC}, infoErr
		}})
	if err != nil {
		return nil, demo{}, nil, err
	}
	shop, err := storefront.NewShop(storefront.ShopDeps{Pool: pool, Redis: rdb, Catalog: products, Stores: stores, Media: media, Log: log,
		Cache: storefront.DefaultCacheConfig(), Orders: shopOrders,
		ClientIP: func(r *http.Request) string {
			if ip, ok := middleware.ClientIP(r.Context()); ok {
				return ip.String()
			}
			return r.RemoteAddr
		}})
	if err != nil {
		return nil, demo{}, nil, err
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{authz.RoleOwner: authz.Permissions()})
	if err != nil {
		return nil, demo{}, nil, err
	}
	members := map[string]testkit.FakeMember{}
	mux := http.NewServeMux()
	cat.Mount(mux, pol, testkit.FakeStoreScope(resp, members), resp)
	look.Mount(mux, pol, testkit.FakeStoreScope(resp, members), resp)
	ship.Mount(mux, pol, testkit.FakeStoreScope(resp, members), resp)
	return shop, demo{h: mux, members: members}, ten, nil
}

func seed(ctx context.Context, pool *pgxpool.Pool, ten *tenancy.Module, d demo, load bool) error {
	id := tenant.StoreID{ID: ulid.NewGenerator().New()}
	sctx := tenant.WithStore(ctx, id)
	err := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
		_, err := ten.Service().CreateStoreTx(sctx, tx, tenancy.NewStore{Slug: "souq", Name: "متجر العرض", Currency: "EGP", Locale: "ar", Timezone: "Africa/Cairo"})
		return err
	})
	if err != nil {
		return err
	}
	d.members["owner"] = testkit.FakeMember{UserID: ulid.NewGenerator().New(), Roles: map[tenant.StoreID]authz.Role{id: authz.RoleOwner}}
	call := func(method, path, body string) (string, error) {
		rq := httptest.NewRequestWithContext(ctx, method, "/v1/stores/"+id.String()+path, strings.NewReader(body))
		rq.Header.Set("Authorization", "Bearer owner")
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		d.h.ServeHTTP(rec, rq)
		if rec.Code >= 300 {
			return "", fmt.Errorf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.String(), nil
	}
	if _, err = call(http.MethodPost, "/categories", `{"name":"عطور","slug":"perfumes"}`); err != nil {
		return err
	}
	first, err := call(http.MethodPost, "/products", `{"title":"عطر العود الملكي","slug":"royal-oud","status":"active",
		"description":"عطر شرقي فاخر بخلاصة العود والعنبر يدوم طوال اليوم.\n\nمناسب للمناسبات والسهرات.",
		"options":[{"name":"الحجم","values":["50 مل","100 مل"]}],
		"variants":[{"option_values":["50 مل"],"price_minor":45000,"compare_at_minor":60000,"on_hand":10},
		            {"option_values":["100 مل"],"price_minor":75000,"on_hand":4}]}`)
	if err != nil {
		return err
	}
	for i, name := range []string{"مسك الطهارة", "عطر الياسمين", "زيت الورد", "بخور المسك", "عطر الصندل", "معطر المنزل", "عطر الفانيليا"} {
		body := fmt.Sprintf(`{"title":%q,"slug":"item-%d","status":"active","description":"منتج مختار بعناية.","variants":[{"price_minor":%d,"on_hand":%d}]}`,
			name, i+1, 9900+i*2500, (i%3)*5)
		if _, err = call(http.MethodPost, "/products", body); err != nil {
			return err
		}
	}
	productID := first[strings.Index(first, `"id":"`)+6:][:26]
	ends := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	steps := []struct{ method, path, body string }{
		{http.MethodPut, "/storefront/home", `{"title":"متجر العرض","description":"عطور وبخور أصلية بأسعار مميزة.","sections":[
			{"type":"hero","settings":{"heading":"أهلًا بك في متجر العرض","subheading":"عطور وبخور أصلية، توصيل لكل المحافظات","button_label":"شاهد عرض الصيف","button_link":"/pages/summer"}},
			{"type":"product_grid","settings":{"source":"newest","limit":8}},
			{"type":"trust_badges","settings":{"items":[{"icon":"cod"},{"icon":"shipping"},{"icon":"returns"}]}}]}`},
		{http.MethodPost, "/storefront/pages", `{"title":"عرض الصيف","slug":"summer","status":"published","sections":[
			{"type":"product_landing","settings":{"product_id":"` + productID + `","headline":"عطر العود الملكي بخصم ٢٥٪","highlights":["ثبات أكثر من ١٢ ساعة","شحن مجاني فوق ١٠٠٠ ج.م","الدفع عند الاستلام"]}},
			{"type":"countdown","settings":{"ends_at":"` + ends + `"}},
			{"type":"reviews","settings":{"items":[{"name":"منى","location":"القاهرة","rating":5,"text":"رائحة رائعة ووصل بسرعة."},{"name":"أحمد","location":"الإسكندرية","rating":4,"text":"جودة ممتازة."}]}},
			{"type":"faq","settings":{"items":[{"question":"متى يصل الطلب؟","answer":"خلال ٢ إلى ٤ أيام عمل."},{"question":"هل يمكن الاسترجاع؟","answer":"نعم، خلال ١٤ يومًا."}]}}]}`},
		// Shipping (ADR-0025): Cairo and Giza 40 in 2–3 days, elsewhere 60,
		// free from 1,000, and a 10 cash-on-delivery fee.
		{http.MethodPost, "/shipping/zones", `{"name":"القاهرة والجيزة","areas":["EG-C","EG-GZ"],"rate_minor":4000,"eta_min_days":2,"eta_max_days":3}`},
		{http.MethodPost, "/shipping/zones", `{"name":"باقي المحافظات","areas":` + otherAreas("EG-C", "EG-GZ") + `,"rate_minor":6000}`},
		{http.MethodPut, "/shipping/settings", `{"version":0,"cod_fee":{"fixed_minor":1000,"bp":0,"min_minor":0,"max_minor":0},"free_over_minor":100000}`},
		{http.MethodPut, "/storefront/policies/refund", `{"title":"سياسة الاسترجاع","body":"يمكنك استرجاع المنتج خلال ١٤ يومًا من الاستلام.\n\nيجب أن يكون المنتج بحالته الأصلية.","status":"published"}`},
	}
	if load {
		steps = append(steps, struct{ method, path, body string }{http.MethodPost, "/products",
			`{"title":"منتج اختبار التحميل","slug":"load-test","status":"active","variants":[{"price_minor":10000,"on_hand":1000000}]}`})
	}
	for _, s := range steps {
		if _, err = call(s.method, s.path, s.body); err != nil {
			return err
		}
	}
	return nil
}

// otherAreas is a JSON array of Egypt's governorates except skip. Codes
// are ISO 3166-2 (letters, digits, a dash): nothing to escape.
func otherAreas(skip ...string) string {
	var codes []string
	for _, a := range geo.Areas(geo.EG) {
		if !slices.Contains(skip, a.Code) {
			codes = append(codes, `"`+a.Code+`"`)
		}
	}
	return "[" + strings.Join(codes, ",") + "]"
}
