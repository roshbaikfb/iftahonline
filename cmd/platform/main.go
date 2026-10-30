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
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions"
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
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
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
	// plan answers what each store's plan allows (ADR-0045); one per
	// process, so its cache is shared by every module.
	plan entitlements.Entitlements
	// entitlements is plan's module: each role listens for its
	// invalidations (ADR-0050).
	entitlements *entitlements.Module
	// watchOrders handles orders.order.placed for payments; the worker
	// sets it once the payments module is built.
	watchOrders *lateHandler
	countSales  *lateHandler
	// confirmOrders handles orders.order.placed for messaging (the
	// order confirmation); the worker sets it.
	confirmOrders *lateHandler
	// releaseCoupons handles orders.order.status_changed for promotions
	// (a cancelled order's coupon use); the worker sets it.
	releaseCoupons *lateHandler
	// reportPurchases handles orders.order.placed for marketing (the
	// server-side Purchase); the worker sets it.
	reportPurchases *lateHandler
	// planLimits handles entitlements.usage.crossed for messaging (the
	// merchant's warning); the worker sets it.
	planLimits *lateHandler
	// sellFees handles orders.order.status_changed for billing (a
	// delivered order's fee); the worker sets it.
	sellFees *lateHandler
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
	// A gateway connected or disconnected: whether checkout offers cards,
	// and the payment methods every page's footer lists (so the edge too).
	if err = bus.Subscribe(payments.EventConnectionChanged, storefront.InvalidateSubscriber, storefront.InvalidateHandler(rdb)); err != nil {
		return clients{}, err
	}
	if err = bus.Subscribe(payments.EventConnectionChanged, storefront.PurgeSubscriber, storefront.PurgeHandler(purger)); err != nil {
		return clients{}, err
	}
	// A pixel or the consent mode changed: every page of the store loads
	// other tags (ADR-0043).
	if err = bus.Subscribe(marketing.EventPixelsChanged, storefront.InvalidateSubscriber, storefront.InvalidateHandler(rdb)); err != nil {
		return clients{}, err
	}
	if err = bus.Subscribe(marketing.EventPixelsChanged, storefront.PurgeSubscriber, storefront.PurgeHandler(purger)); err != nil {
		return clients{}, err
	}
	// The product feeds' key changed: the edge's copies under the old one
	// go (ADR-0044).
	if err = bus.Subscribe(marketing.EventFeedChanged, storefront.PurgeSubscriber, storefront.PurgeHandler(purger)); err != nil {
		return clients{}, err
	}
	watch := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderPlaced, payments.WatchOrdersSubscriber, watch.Handle); err != nil {
		return clients{}, err
	}
	// Bestsellers follow orders' statuses (ADR-0033).
	sales := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderStatusChanged, orders.SalesSubscriber, sales.Handle); err != nil {
		return clients{}, err
	}
	// Cash-on-delivery customers confirm by message (ADR-0040).
	confirm := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderPlaced, messaging.ConfirmOrdersSubscriber, confirm.Handle); err != nil {
		return clients{}, err
	}
	// A cancelled order gives its coupon's use back (ADR-0042).
	release := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderStatusChanged, promotions.ReleaseCouponsSubscriber, release.Handle); err != nil {
		return clients{}, err
	}
	// A placed order's Purchase goes to the store's ad providers (ADR-0043).
	purchases := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderPlaced, marketing.PurchasesSubscriber, purchases.Handle); err != nil {
		return clients{}, err
	}
	// A plan's monthly limit near or reached: the merchant hears (ADR-0045).
	limits := &lateHandler{}
	if err = bus.Subscribe(entitlements.EventUsageCrossed, messaging.PlanLimitsSubscriber, limits.Handle); err != nil {
		return clients{}, err
	}
	// A store's plan changed: what it may do, and so its pages (cards,
	// pixels, ads), on every instance and at the edge (ADR-0050).
	if err = bus.Subscribe(billing.EventSubscriptionChanged, entitlements.InvalidateSubscriber, entitlements.InvalidateHandler(rdb)); err != nil {
		return clients{}, err
	}
	if err = bus.Subscribe(billing.EventSubscriptionChanged, storefront.InvalidateSubscriber, storefront.InvalidateHandler(rdb)); err != nil {
		return clients{}, err
	}
	if err = bus.Subscribe(billing.EventSubscriptionChanged, storefront.PurgeSubscriber, storefront.PurgeHandler(purger)); err != nil {
		return clients{}, err
	}
	// A delivered or returned order's pay-as-you-sell fee (ADR-0048).
	fees := &lateHandler{}
	if err = bus.Subscribe(orders.EventOrderStatusChanged, billing.SellFeesSubscriber, fees.Handle); err != nil {
		return clients{}, err
	}
	bill, err := billing.New(billing.Deps{Pool: pool})
	if err != nil {
		return clients{}, err
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: pool, Source: entitlements.FromBilling(bill.Grants()), IDs: ids, Events: bus})
	if err != nil {
		return clients{}, err
	}
	return clients{cfg: cfg, log: log, ids: ids, pool: pool, rdb: rdb, events: bus, plan: ent.Entitlements(), entitlements: ent, watchOrders: watch, countSales: sales,
		confirmOrders: confirm, releaseCoupons: release, reportPurchases: purchases, planLimits: limits, sellFees: fees}, nil
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
		Audit: identity.NewAuditRecorder(c.ids), AdminPool: c.adminPool, Plan: c.plan,
	})
}

func newIdentity(c clients, ten *tenancy.Module, onb *onboarding.Module) (*identity.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return identity.New(identity.Deps{
		Pool: c.pool, Redis: c.rdb, Tenancy: ten.Service(), Onboarding: onb.Starter(), IDs: c.ids, Log: c.log,
		Config: c.cfg.Identity, ClientIP: middleware.ClientIP,
		Secrets: c.cfg.Secrets, Jobs: inserter, Env: c.cfg.Env, Plan: c.plan,
	})
}

// newOnboarding builds the setup wizard module (ADR-0034). The worker
// passes the catalog and storefront, through which it applies starter
// templates; the api role, which only enqueues them, passes nil.
func newOnboarding(c clients, ten *tenancy.Module, cat *catalog.Module, shop *storefront.Module) (*onboarding.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	d := onboarding.Deps{Pool: c.pool, Jobs: inserter, Audit: identity.NewAuditRecorder(c.ids)}
	if cat != nil && shop != nil && ten != nil {
		stores := ten.Service()
		d.Catalog, d.Storefront = cat.Starter(), shop.Starter()
		d.Currency = func(ctx context.Context) (money.Currency, error) {
			st, err := stores.GetStore(ctx)
			return st.Currency, err
		}
	}
	return onboarding.New(d)
}

func newCatalog(c clients, ten *tenancy.Module) (*catalog.Module, error) {
	inserter, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	stores := ten.Service()
	return catalog.New(catalog.Deps{
		Pool: c.pool, Jobs: inserter, IDs: c.ids, Log: c.log, Audit: identity.NewAuditRecorder(c.ids), Media: c.cfg.Media, Search: c.cfg.Search, Keyring: c.cfg.Secrets.Keyring,
		Events: c.events, Plan: c.plan,
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
// newMessaging builds the messaging module with its quotas, read from
// each store's plan (api and worker roles). inbox is the worker's, for webhook jobs, and conf its
// order confirmation's collaborators; both nil in the api.
func newMessaging(c clients, inbox webhooks.Inbox, conf *messaging.Deps) (*messaging.Module, error) {
	ins, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	d := messaging.Deps{
		Pool: c.pool, IDs: c.ids, Secrets: c.cfg.Secrets, Config: c.cfg.Messaging, Quota: c.plan, Jobs: ins,
		PublicURL: c.cfg.Mail.DashboardURL, Inbox: inbox, Audit: identity.NewAuditRecorder(c.ids), Log: c.log,
	}
	if conf != nil {
		d.Orders, d.Stores, d.Contacts, d.Mailer, d.DashboardURL = conf.Orders, conf.Stores, conf.Contacts, conf.Mailer, conf.DashboardURL
	}
	return messaging.New(d)
}

// messagingStores adapts tenancy to what a confirmation needs: the
// store's name, locale and primary host (for the SMS link).
func messagingStores(ten *tenancy.Module) messaging.StoreSource {
	stores := ten.Service()
	return func(ctx context.Context) (messaging.StoreInfo, error) {
		st, err := stores.GetStore(ctx)
		if err != nil {
			return messaging.StoreInfo{}, err
		}
		domains, err := stores.ListDomains(ctx)
		if err != nil {
			return messaging.StoreInfo{}, err
		}
		info := messaging.StoreInfo{Name: st.Name, Locale: string(st.Locale)}
		for _, d := range domains {
			if d.IsPrimary {
				info.Host = d.Host
			}
		}
		if info.Host == "" {
			return messaging.StoreInfo{}, fmt.Errorf("messaging: store %s has no primary host", st.Slug)
		}
		return info, nil
	}
}

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

func newOrders(c clients, ten *tenancy.Module, cat *catalog.Module, cust *customers.Module, ship *shipping.Module, prom *promotions.Module,
	auth authz.Authorizer) (*orders.Module, error) {
	return orders.New(orders.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events,
		Catalog: cat.Catalog(), Customers: cust.Customers(), Shipping: ship.Quoter(), Coupons: orderCoupons{prom.Coupons()}, Auth: auth,
		Stores: orderStores(ten), Plan: c.plan,
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
	prom, err := newPromotions(c)
	if err != nil {
		return orders.ShopDeps{}, err
	}
	return orders.ShopDeps{Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events,
		Catalog: stock, Customers: cust.Customers(), Shipping: ship.Quoter(), Coupons: orderCoupons{prom.Coupons()}, Stores: orderStores(ten), Plan: c.plan}, nil
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
	onb, err := newOnboarding(c, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	id, err := newIdentity(c, ten, onb)
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
	go c.entitlements.Listen(ctx, c.rdb, c.log)
	prom, err := newPromotions(c)
	if err != nil {
		return nil, err
	}
	ord, err := newOrders(c, ten, cat, cust, ship, prom, id.Authorizer())
	if err != nil {
		return nil, err
	}
	shipOrders.f = ord.Fulfilment()
	hooks, err := newWebhooks(c)
	if err != nil {
		return nil, err
	}
	pay, err := newPayments(c, ord.Payments(), nil, ten)
	if err != nil {
		return nil, err
	}
	for _, p := range append(ship.WebhookProviders(), pay.WebhookProviders()...) {
		if err = hooks.Register(p); err != nil {
			return nil, err
		}
	}
	msg, err := newMessaging(c, nil, nil)
	if err != nil {
		return nil, err
	}
	mkt, err := newMarketing(c, nil)
	if err != nil {
		return nil, err
	}
	for _, p := range msg.PlatformProviders() {
		if err = hooks.RegisterPlatform(p); err != nil {
			return nil, err
		}
	}
	billJobs, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	bill, err := billing.New(billing.Deps{Pool: c.pool, Jobs: billJobs, IDs: c.ids, Log: c.log, Paymob: c.cfg.Billing,
		PublicURL: c.cfg.Mail.DashboardURL, DashboardURL: c.cfg.Mail.DashboardURL})
	if err != nil {
		return nil, err
	}
	// Paymob's callbacks of merchants' checkouts (ADR-0053).
	if p, ok := bill.CallbackProvider(); ok {
		if err = hooks.RegisterPlatform(p); err != nil {
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
		onb.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		cat.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		shop.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		cust.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		ord.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		ship.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		pay.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		msg.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		prom.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		mkt.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		bill.Mount(rt.Mux, id.Authorizer(), id.Scope(), rt.Problems)
		if mountErr = pay.MountCheckout(rt.Mux, rt.Problems); mountErr != nil {
			c.log.Error("card checkout not mounted", "err", mountErr)
		}
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
	products, err := catalog.NewStorefront(catalog.StorefrontDeps{Pool: c.pool, Media: c.cfg.Media, Search: c.cfg.Search})
	if err != nil {
		return nil, err
	}
	ship, err := newShipping(c, ten, nil, nil)
	if err != nil {
		return nil, err
	}
	go ship.Listen(ctx)
	go c.entitlements.Listen(ctx, c.rdb, c.log)
	orderDeps, err := shopOrderDeps(c, ten, ship)
	if err != nil {
		return nil, err
	}
	shopOrders, err := orders.NewShop(orderDeps)
	if err != nil {
		return nil, err
	}
	fulfilment, err := orders.NewFulfilment(orderDeps)
	if err != nil {
		return nil, err
	}
	ins, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	links, err := messaging.NewConfirmLinks(messaging.LinksDeps{Number: c.cfg.Messaging.WhatsAppDisplayNumber, Pool: c.pool, IDs: c.ids,
		Jobs: ins, Orders: fulfilment, Log: c.log})
	if err != nil {
		return nil, err
	}
	mkt, err := newMarketing(c, nil)
	if err != nil {
		return nil, err
	}
	shop, err := storefront.NewShop(storefront.ShopDeps{
		Confirmations: links, Pixels: mkt.Pixels(), Conversions: mkt.Conversions(), Feeds: mkt.Feeds(),
		Orders: shopOrders, Parcels: ship.Tracker(), Captcha: c.cfg.Captcha, Cards: payments.NewCardCheck(c.pool, c.plan), CheckoutURL: c.cfg.Payments.CheckoutURL,
		Pool: c.pool, Redis: c.rdb, Catalog: products, Stores: ten.Service(), Media: c.cfg.Media, Log: c.log,
		Cache: storefront.DefaultCacheConfig(), HTTPS: c.cfg.Env == config.EnvProduction || c.cfg.Env == config.EnvStaging,
		EdgeTTL: c.cfg.Edge.CacheTTL, Search: c.cfg.Search.URL != "",
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
	go c.entitlements.Listen(ctx, rdb, log)
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
	onb, err := newOnboarding(c, ten, cat, shop)
	if err != nil {
		return err
	}
	if err = onb.RegisterWorkers(workers); err != nil {
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
	ords, err := orders.NewFulfilment(orderDeps)
	if err != nil {
		return err
	}
	shipOrders.f = ords
	if err = ship.RegisterWorkers(workers); err != nil {
		return err
	}
	pay, err := newPayments(c, ords, hooks.Inbox(), nil)
	if err != nil {
		return err
	}
	if err = pay.RegisterWorkers(workers); err != nil {
		return err
	}
	c.watchOrders.Set(pay.OrderPlacedHandler())
	msg, err := newMessaging(c, hooks.Inbox(), &messaging.Deps{
		Orders: ords, Stores: messagingStores(ten), Contacts: identity.StaffEmails(pool, authz.RoleOwner, authz.RoleManager),
		Mailer: mailer.FromConfig(cfg.Mail, log), DashboardURL: cfg.Mail.DashboardURL,
	})
	if err != nil {
		return err
	}
	if err = msg.RegisterWorkers(workers); err != nil {
		return err
	}
	c.confirmOrders.Set(msg.OrderPlacedHandler())
	c.planLimits.Set(msg.PlanLimitHandler())
	prom, err := newPromotions(c)
	if err != nil {
		return err
	}
	c.releaseCoupons.Set(prom.OrderStatusHandler())
	convOrders, err := orders.NewConversions(orderDeps)
	if err != nil {
		return err
	}
	mkt, err := newMarketing(c, convOrders)
	if err != nil {
		return err
	}
	if err = mkt.AddWorkers(workers); err != nil {
		return err
	}
	c.reportPurchases.Set(mkt.OrderPlacedHandler())
	counter, err := orders.NewSalesCounter(orderDeps)
	if err != nil {
		return err
	}
	c.countSales.Set(counter)
	billJobs, err := server.NewRiverInserter(pool, log)
	if err != nil {
		return err
	}
	billSealer, err := envelope.New(envelope.FromKeyring(cfg.Secrets.Keyring))
	if err != nil {
		return err
	}
	bill, err := billing.New(billing.Deps{Pool: pool, Orders: billingOrders(ords), Stores: billingStores(ten), Jobs: billJobs, IDs: c.ids, Events: c.events,
		Sealer: billSealer, Holds: billingHolds(ten), Collect: cfg.Billing.CollectionEnabled, Paymob: cfg.Billing,
		Inbox: hooks.Inbox(), Log: log, PublicURL: cfg.Mail.DashboardURL, DashboardURL: cfg.Mail.DashboardURL,
		Notifier: billingNotifier(ten, identity.StaffEmails(pool, authz.RoleOwner), mailer.FromConfig(cfg.Mail, log), cfg.Mail.DashboardURL)})
	if err != nil {
		return err
	}
	c.sellFees.Set(sellFeesHandler(bill.Fees()))
	if err = bill.RegisterWorkers(workers); err != nil {
		return err
	}
	if err = identity.RegisterWorkers(workers, identity.WorkerDeps{
		Pool: pool, Redis: rdb, Secrets: cfg.Secrets, Mail: cfg.Mail, Env: cfg.Env, Tenancy: ten.Service(), Log: log,
	}); err != nil {
		return err
	}
	client, err := server.NewRiverClient(pool, cfg.Worker, log, workers, cat.Queues(), append(cat.PeriodicJobs(), bill.PeriodicJobs()...)...)
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
