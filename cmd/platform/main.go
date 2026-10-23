// Command platform is the single binary for every role (ADR-0001):
//
//	platform api                    merchant/admin JSON API
//	platform storefront             server-rendered shop pages
//	platform worker                 River background jobs
//	platform migrate up|down|status database migrations (as app_owner)
//	platform admin create --email E --name N
//	                                add a platform admin (password on stdin;
//	                                prints the TOTP enrolment once)
//	platform version                print build information
//
// Configuration comes only from environment variables (see
// internal/platform/config). Logs are JSON on stdout.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/cdn"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/otel"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

// Set at link time: -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"  //nolint:gochecknoglobals // link-time build metadata.
	commit  = "none" //nolint:gochecknoglobals // link-time build metadata.
)

const usage = `usage: platform <api|storefront|worker|migrate up|down|status|admin create --email E --name N|version>`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.LookupEnv))
}

// run is main without process exit, so it can be tested.
func run(args []string, stdin io.Reader, stdout io.Writer, lookup config.LookupFunc) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	if args[0] == "version" {
		return printVersion(stdout)
	}

	role := config.Role(args[0])
	boot := logging.New(logging.Options{Writer: stdout, Level: slog.LevelInfo, Service: "iftahonline", Role: string(role), Version: version})
	cfg, err := config.Load(role, lookup)
	if err != nil {
		boot.Error("startup failed", "err", err)
		return 1
	}
	log := logging.New(logging.Options{Writer: stdout, Level: cfg.LogLevel, Service: "iftahonline", Role: string(role), Version: version})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // a second signal now kills the process immediately
	}()

	build := server.BuildInfo{Version: version, Commit: commit, Role: string(role)}
	log.Info("starting", "config", cfg, "gomaxprocs", runtime.GOMAXPROCS(0), "gomemlimit", debug.SetMemoryLimit(-1))

	switch role {
	case config.RoleAPI:
		err = runHTTP(ctx, cfg, log, build, middleware.ProfileAPI, mountAPI)
	case config.RoleStorefront:
		err = runHTTP(ctx, cfg, log, build, middleware.ProfileStorefront, mountStorefront)
	case config.RoleWorker:
		err = runWorker(ctx, cfg, log, build)
	case config.RoleMigrate:
		err = runMigrate(ctx, cfg, log, args[1:], stdout)
	case config.RoleAdmin:
		err = runAdmin(ctx, cfg, log, args[1:], stdin, stdout)
	}
	if err != nil {
		log.Error("exited with error", "err", err)
		return 1
	}
	log.Info("exited cleanly")
	return 0
}

func printVersion(w io.Writer) int {
	err := json.NewEncoder(w).Encode(map[string]string{"version": version, "commit": commit, "go_version": runtime.Version()})
	if err != nil {
		return 1
	}
	return 0
}

// deps are the shared clients every long-running role needs.
type deps struct {
	tel *otel.Telemetry
	ids *ulid.Generator
}

func newDeps(ctx context.Context, cfg config.Config, build server.BuildInfo) (deps, error) {
	tel, err := otel.Setup(ctx, otel.Options{
		Endpoint: cfg.OTel.Endpoint, ServiceName: "iftahonline-" + build.Role, Version: build.Version,
		Environment: cfg.Env, SampleRatio: cfg.OTel.SampleRatio,
	})
	if err != nil {
		return deps{}, fmt.Errorf("telemetry: %w", err)
	}
	return deps{tel: tel, ids: ulid.NewGenerator()}, nil
}

func (d deps) close(ctx context.Context, log *slog.Logger) {
	if err := d.tel.Shutdown(context.WithoutCancel(ctx)); err != nil {
		log.Warn("telemetry shutdown", "err", err)
	}
}

func listen(ctx context.Context, addr string) (net.Listener, error) {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return ln, nil
}

// clients are the shared connections a role's modules are built from.
type clients struct {
	cfg       config.Config
	log       *slog.Logger
	ids       *ulid.Generator
	pool      *pgxpool.Pool
	rdb       *redis.Client
	adminPool *pgxpool.Pool // api role with ADMIN_DATABASE_URL only
	events    *eventbus.Bus // one per process; subscriptions are made in newClients
}

// newClients builds the shared clients and the process's event bus. Every
// role makes the same subscriptions (ADR-0015): publishers need the
// subscriber list, the worker needs the handlers.
func newClients(cfg config.Config, log *slog.Logger, ids *ulid.Generator, pool *pgxpool.Pool, rdb *redis.Client) (clients, error) {
	inserter, err := server.NewRiverInserter(pool, log)
	if err != nil {
		return clients{}, err
	}
	// Only the worker runs handlers; the other roles need the same
	// subscriber list to publish, so their purger is a no-op.
	var purger cdn.Purger = cdn.Nop{}
	if cfg.Edge.Purges() {
		if purger, err = cdn.NewCloudflare(cdn.CloudflareOptions{ZoneID: cfg.Edge.CloudflareZoneID, Token: cfg.Edge.CloudflareToken}); err != nil {
			return clients{}, err
		}
	}
	bus := eventbus.New(inserter)
	for _, typ := range storefront.InvalidatingEvents() {
		if err = bus.Subscribe(typ, storefront.InvalidateSubscriber, storefront.InvalidateHandler(rdb)); err != nil {
			return clients{}, err
		}
		if err = bus.Subscribe(typ, storefront.PurgeSubscriber, storefront.PurgeHandler(purger)); err != nil {
			return clients{}, err
		}
	}
	if err = bus.Subscribe(shipping.EventRatesChanged, shipping.InvalidateSubscriber, shipping.InvalidateHandler(rdb)); err != nil {
		return clients{}, err
	}
	return clients{cfg: cfg, log: log, ids: ids, pool: pool, rdb: rdb, events: bus}, nil
}

// mountFunc builds a role's modules and returns its routes. Background
// work it starts must stop when ctx ends.
type mountFunc func(ctx context.Context, c clients) (func(server.Routes), error)

func newTenancy(c clients) (*tenancy.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return tenancy.New(tenancy.Deps{
		Pool: c.pool, Redis: c.rdb, Jobs: inserter, IDs: c.ids, Log: c.log, Config: c.cfg.Tenancy,
		Audit: identity.NewAuditRecorder(c.ids), AdminPool: c.adminPool,
	})
}

func newIdentity(c clients, ten *tenancy.Module) (*identity.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return identity.New(identity.Deps{
		Pool: c.pool, Redis: c.rdb, Tenancy: ten.Service(), IDs: c.ids, Log: c.log,
		Config: c.cfg.Identity, ClientIP: middleware.ClientIP,
		Secrets: c.cfg.Secrets, Jobs: inserter, Env: c.cfg.Env,
	})
}

func newCatalog(c clients, ten *tenancy.Module) (*catalog.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	stores := ten.Service()
	return catalog.New(catalog.Deps{
		Pool: c.pool, Jobs: inserter, IDs: c.ids, Log: c.log, Audit: identity.NewAuditRecorder(c.ids), Media: c.cfg.Media, Search: c.cfg.Search, Keyring: c.cfg.Secrets.Keyring,
		Events: c.events,
		Currency: catalog.CurrencyFunc(func(ctx context.Context) (money.Currency, error) {
			st, err := stores.GetStore(ctx)
			return st.Currency, err
		}),
	})
}

// newShipping builds the shipping module. Every role that quotes keeps
// its cache fresh with Listen.
func newShipping(c clients, ten *tenancy.Module, ords shipping.Orders, inbox webhooks.Inbox) (*shipping.Module, error) {
	stores := ten.Service()
	d := shipping.Deps{
		Pool: c.pool, IDs: c.ids, Redis: c.rdb, Log: c.log, Audit: identity.NewAuditRecorder(c.ids), Events: c.events,
		Cache: shipping.DefaultCacheConfig(), Carriers: c.cfg.Shipping, PublicURL: c.cfg.Mail.DashboardURL, Orders: ords, Inbox: inbox,
		Stores: func(ctx context.Context) (shipping.StoreInfo, error) {
			st, err := stores.GetStore(ctx)
			if err != nil {
				return shipping.StoreInfo{}, err
			}
			country, ok := geo.CountryOf(st.Currency)
			if !ok {
				return shipping.StoreInfo{}, fmt.Errorf("shipping: no country for currency %q", st.Currency)
			}
			loc, err := time.LoadLocation(st.Timezone)
			if err != nil {
				loc = time.UTC
			}
			return shipping.StoreInfo{Currency: st.Currency, Country: country, Location: loc}, nil
		},
	}
	if c.cfg.Secrets.Keyring != nil { // the api and worker roles: they connect carriers, ship and store labels
		var err error
		if d.Sealer, err = envelope.New(envelope.FromKeyring(c.cfg.Secrets.Keyring)); err != nil {
			return nil, err
		}
		if d.Objects, err = media.NewS3For(c.cfg.Media); err != nil {
			return nil, err
		}
		if d.Jobs, err = server.NewRiverInserter(c.pool, c.log); err != nil {
			return nil, err
		}
	}
	return shipping.New(d)
}

// newWebhooks builds the inbound webhook inbox. Carriers and gateways
// register their providers on it (P5).
func newWebhooks(c clients) (*webhooks.Module, error) {
	limiter, err := ratelimit.New(c.rdb, "webhooks")
	if err != nil {
		return nil, err
	}
	return webhooks.New(webhooks.Deps{Pool: c.pool, IDs: c.ids, Limiter: limiter, Log: c.log, ClientIP: middleware.ClientIP})
}

func newCustomers(c clients, ten *tenancy.Module) (*customers.Module, error) {
	stores := ten.Service()
	return customers.New(customers.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids),
		Region: func(ctx context.Context) (phone.Region, error) {
			st, err := stores.GetStore(ctx)
			if err != nil {
				return "", err
			}
			country, ok := geo.CountryOf(st.Currency)
			if !ok {
				return "", fmt.Errorf("customers: no country for currency %q", st.Currency)
			}
			return phone.Region(country), nil
		},
	})
}

func newOrders(c clients, ten *tenancy.Module, cat *catalog.Module, cust *customers.Module, ship *shipping.Module, auth authz.Authorizer) (*orders.Module, error) {
	return orders.New(orders.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events,
		Catalog: cat.Catalog(), Customers: cust.Customers(), Shipping: ship.Quoter(), Auth: auth, Stores: orderStores(ten),
	})
}

// orderStores adapts tenancy to what orders need: currency, country (from
// the currency) and time zone.
func orderStores(ten *tenancy.Module) orders.StoreSource {
	stores := ten.Service()
	return func(ctx context.Context) (orders.StoreInfo, error) {
		st, err := stores.GetStore(ctx)
		if err != nil {
			return orders.StoreInfo{}, err
		}
		country, ok := geo.CountryOf(st.Currency)
		if !ok {
			return orders.StoreInfo{}, fmt.Errorf("orders: no country for currency %q", st.Currency)
		}
		loc, err := time.LoadLocation(st.Timezone)
		if err != nil {
			loc = time.UTC
		}
		return orders.StoreInfo{Currency: st.Currency, Country: country, Location: loc}, nil
	}
}

// shopOrderDeps are what orders need outside the merchant API: stock and
// prices (not the whole catalog), customers, and shipping's quoter.
func shopOrderDeps(c clients, ten *tenancy.Module, ship *shipping.Module) (orders.ShopDeps, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return orders.ShopDeps{}, err
	}
	stock, err := catalog.NewStock(catalog.StockDeps{Pool: c.pool, Jobs: inserter, IDs: c.ids, Events: c.events})
	if err != nil {
		return orders.ShopDeps{}, err
	}
	cust, err := newCustomers(c, ten)
	if err != nil {
		return orders.ShopDeps{}, err
	}
	return orders.ShopDeps{Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events,
		Catalog: stock, Customers: cust.Customers(), Shipping: ship.Quoter(), Stores: orderStores(ten)}, nil
}

// newShopOrders builds the orders module's shopper side (storefront role).
func newShopOrders(c clients, ten *tenancy.Module, ship *shipping.Module) (orders.Shop, error) {
	d, err := shopOrderDeps(c, ten, ship)
	if err != nil {
		return nil, err
	}
	return orders.NewShop(d)
}

func newStorefront(c clients) (*storefront.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return storefront.New(storefront.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events, Jobs: inserter, Media: c.cfg.Media,
	})
}

// mountAPI wires the merchant API: identity's routes, and every store
// module's routes behind identity's membership middleware and policy.
func mountAPI(ctx context.Context, c clients) (func(server.Routes), error) {
	ten, err := newTenancy(c)
	if err != nil {
		return nil, err
	}
	id, err := newIdentity(c, ten)
	if err != nil {
		return nil, err
	}
	cat, err := newCatalog(c, ten)
	if err != nil {
		return nil, err
	}
	shop, err := newStorefront(c)
	if err != nil {
		return nil, err
	}
	cust, err := newCustomers(c, ten)
	if err != nil {
		return nil, err
	}
	shipOrders := &shippingOrders{}
	ship, err := newShipping(c, ten, shipOrders, nil)
	if err != nil {
		return nil, err
	}
	go ship.Listen(ctx)
	ord, err := newOrders(c, ten, cat, cust, ship, id.Authorizer())
	if err != nil {
		return nil, err
	}
	shipOrders.f = ord.Fulfilment()
	hooks, err := newWebhooks(c)
	if err != nil {
		return nil, err
	}
	for _, p := range ship.WebhookProviders() {
		if err = hooks.Register(p); err != nil {
			return nil, err
		}
	}
	var console *admin.Module
	if c.adminPool != nil {
		if console, err = admin.New(admin.Deps{
			AdminPool: c.adminPool, Redis: c.rdb, Directory: ten.Directory(), Stores: ten.Service(), IDs: c.ids, Log: c.log,
			Secrets: c.cfg.Secrets, Hashing: c.cfg.Identity, AdminHost: c.cfg.Tenancy.AdminHost, ClientIP: middleware.ClientIP,
		}); err != nil {
			return nil, err
		}
	} else {
		c.log.Info("super-admin console disabled (no ADMIN_DATABASE_URL)")
	}
	var mountErr error
	return func(rt server.Routes) {
		id.Mount(rt.Mux, rt.Problems)
		ten.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		cat.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		shop.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		cust.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		ord.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		ship.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		hooks.Mount(rt.Mux, rt.Problems)
		if console != nil {
			mountErr = console.Mount(rt.Mux, rt.Problems)
			if mountErr != nil {
				c.log.Error("admin console not mounted", "err", mountErr)
			}
		}
	}, nil
}

// runAdmin implements `platform admin create --email E --name N`. The
// password is read from stdin (one line), so it never appears in shell
// history or the process list. The TOTP seed is printed once, to stdout
// only; it is never logged.
func runAdmin(ctx context.Context, cfg config.Config, log *slog.Logger, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "admin email")
	name := fs.String("name", "", "admin display name")
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("%s", usage)
	}
	if err := fs.Parse(args[1:]); err != nil || *email == "" || *name == "" {
		return fmt.Errorf("%s", usage)
	}
	pw, err := bufio.NewReader(io.LimitReader(stdin, 1024)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("admin: read password: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DB, "iftahonline-admin-cli")
	if err != nil {
		return err
	}
	defer pool.Close()
	mod, err := admin.New(admin.Deps{AdminPool: pool, IDs: ulid.NewGenerator(), Log: log, Secrets: cfg.Secrets, Hashing: cfg.Identity})
	if err != nil {
		return err
	}
	created, err := mod.CreateAdmin(ctx, *email, *name, strings.TrimRight(pw, "\r\n"))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "admin created: %s (%s)\n\nAdd this account to an authenticator app now; it is shown only once.\nsecret: %s\nuri:    %s\n",
		created.Admin.Email, created.Admin.ID, created.Secret, created.URI)
	return err
}

func mountStorefront(ctx context.Context, c clients) (func(server.Routes), error) {
	ten, err := newTenancy(c)
	if err != nil {
		return nil, err
	}
	go ten.ListenInvalidations(ctx)
	products, err := catalog.NewStorefront(catalog.StorefrontDeps{Pool: c.pool, Media: c.cfg.Media})
	if err != nil {
		return nil, err
	}
	ship, err := newShipping(c, ten, nil, nil)
	if err != nil {
		return nil, err
	}
	go ship.Listen(ctx)
	shopOrders, err := newShopOrders(c, ten, ship)
	if err != nil {
		return nil, err
	}
	shop, err := storefront.NewShop(storefront.ShopDeps{
		Orders: shopOrders, Captcha: c.cfg.Captcha,
		Pool: c.pool, Redis: c.rdb, Catalog: products, Stores: ten.Service(), Media: c.cfg.Media, Log: c.log,
		Cache: storefront.DefaultCacheConfig(), HTTPS: c.cfg.Env == config.EnvProduction || c.cfg.Env == config.EnvStaging,
		EdgeTTL: c.cfg.Edge.CacheTTL,
		ClientIP: func(r *http.Request) string {
			if ip, ok := middleware.ClientIP(r.Context()); ok {
				return ip.String()
			}
			return ""
		},
	})
	if err != nil {
		return nil, err
	}
	go shop.Listen(ctx)
	return server.MountStorefront(ten.Resolver(), shop.Pages(), storefront.AssetsPrefix, shop.Assets()), nil
}

func runHTTP(ctx context.Context, cfg config.Config, log *slog.Logger, build server.BuildInfo, profile middleware.Profile, mountFor mountFunc) error {
	d, err := newDeps(ctx, cfg, build)
	if err != nil {
		return err
	}
	defer d.close(ctx, log)

	pool, err := postgres.NewPool(ctx, cfg.DB, "iftahonline-"+build.Role)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := redisx.New(cfg.Redis)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rdb.Close(); cerr != nil {
			log.Warn("redis close", "err", cerr)
		}
	}()

	c, err := newClients(cfg, log, d.ids, pool, rdb)
	if err != nil {
		return err
	}
	if cfg.AdminDB.URL != "" {
		adminPool, poolErr := postgres.NewPool(ctx, cfg.AdminDB, "iftahonline-admin")
		if poolErr != nil {
			return poolErr
		}
		defer adminPool.Close()
		c.adminPool = adminPool
	}
	mount, err := mountFor(ctx, c)
	if err != nil {
		return err
	}
	health := server.NewHealth(log, build,
		server.Check{Name: "postgres", Fn: pool.Ping},
		server.Check{Name: "redis", Fn: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
	)
	handler := server.NewHandler(server.Deps{
		Log: log, IDs: d.ids, Telemetry: d.tel, HTTP: cfg.HTTP, Health: health, Profile: profile, Mount: mount,
	})
	ln, err := listen(ctx, cfg.HTTP.Addr)
	if err != nil {
		return err
	}
	return server.Serve(ctx, server.NewServer(cfg.HTTP, handler, log), ln, health, cfg.HTTP, log)
}

func runWorker(ctx context.Context, cfg config.Config, log *slog.Logger, build server.BuildInfo) error {
	d, err := newDeps(ctx, cfg, build)
	if err != nil {
		return err
	}
	defer d.close(ctx, log)

	pool, err := postgres.NewPool(ctx, cfg.DB, "iftahonline-worker")
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := redisx.New(cfg.Redis)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rdb.Close(); cerr != nil {
			log.Warn("redis close", "err", cerr)
		}
	}()

	workers := river.NewWorkers()
	c, err := newClients(cfg, log, d.ids, pool, rdb)
	if err != nil {
		return err
	}
	if err = river.AddWorkerSafely(workers, c.events.Worker()); err != nil {
		return fmt.Errorf("event bus: %w", err)
	}
	ten, err := newTenancy(c)
	if err != nil {
		return err
	}
	if err = ten.RegisterWorkers(workers); err != nil {
		return err
	}
	cat, err := newCatalog(c, ten)
	if err != nil {
		return err
	}
	if err = cat.RegisterWorkers(workers); err != nil {
		return err
	}
	shop, err := newStorefront(c)
	if err != nil {
		return err
	}
	if err = shop.RegisterWorkers(workers); err != nil {
		return err
	}
	hooks, err := newWebhooks(c)
	if err != nil {
		return err
	}
	shipOrders := &shippingOrders{}
	ship, err := newShipping(c, ten, shipOrders, hooks.Inbox())
	if err != nil {
		return err
	}
	orderDeps, err := shopOrderDeps(c, ten, ship)
	if err != nil {
		return err
	}
	if shipOrders.f, err = orders.NewFulfilment(orderDeps); err != nil {
		return err
	}
	if err = ship.RegisterWorkers(workers); err != nil {
		return err
	}
	if err = identity.RegisterWorkers(workers, identity.WorkerDeps{
		Pool: pool, Redis: rdb, Secrets: cfg.Secrets, Mail: cfg.Mail, Env: cfg.Env, Tenancy: ten.Service(), Log: log,
	}); err != nil {
		return err
	}
	client, err := server.NewRiverClient(pool, cfg.Worker, log, workers, cat.Queues(), cat.PeriodicJobs()...)
	if err != nil {
		return err
	}
	// River's start context is detached: cancelling it would hard-stop
	// running jobs. Shutdown is driven by Stop below instead.
	if err = client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	health := server.NewHealth(log, build,
		server.Check{Name: "postgres", Fn: pool.Ping},
		server.Check{Name: "redis", Fn: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
		server.RiverSchemaCheck(pool),
	)
	handler := server.NewHandler(server.Deps{
		Log: log, IDs: d.ids, Telemetry: d.tel, HTTP: cfg.HTTP, Health: health, Profile: middleware.ProfileAPI,
	})
	ln, err := listen(ctx, cfg.HTTP.Addr)
	if err != nil {
		return errors.Join(err, client.StopAndCancel(context.WithoutCancel(ctx)))
	}
	serveErr := server.Serve(ctx, server.NewServer(cfg.HTTP, handler, log), ln, health, cfg.HTTP, log)

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	log.Info("stopping river: finishing running jobs", "timeout", cfg.HTTP.ShutdownTimeout.String())
	if stopErr := client.Stop(stopCtx); stopErr != nil {
		log.Warn("river graceful stop timed out; cancelling jobs", "err", stopErr)
		if cancelErr := client.StopAndCancel(context.WithoutCancel(ctx)); cancelErr != nil {
			return errors.Join(serveErr, fmt.Errorf("river stop: %w", cancelErr))
		}
	}
	return serveErr
}

func runMigrate(ctx context.Context, cfg config.Config, log *slog.Logger, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%s", usage)
	}
	return postgres.Migrate(ctx, cfg.DB.URL, postgres.MigrateCommand(args[0]), log, stdout)
}
