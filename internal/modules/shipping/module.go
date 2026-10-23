package shipping

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/bosta"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// InvalidateChannel is the Redis channel on which instances learn that a
// store's shipping config changed (payload: the store ID).
const InvalidateChannel = "shipping:invalidate"

// InvalidateSubscriber is the event bus subscriber name of
// InvalidateHandler.
const InvalidateSubscriber = "shipping.invalidate_quotes"

// InvalidateHandler tells every instance that the event's store changed
// its shipping. Subscribe it to EventRatesChanged on every role's bus.
func InvalidateHandler(rdb *redis.Client) eventbus.Handler {
	return func(ctx context.Context, d eventbus.Delivery) error {
		return rdb.Publish(ctx, InvalidateChannel, d.Meta.StoreID.String()).Err()
	}
}

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool  *pgxpool.Pool
	IDs   *ulid.Generator
	Redis *redis.Client
	Log   *slog.Logger
	// Audit records changes (identity's audit log); nil discards.
	Audit audit.Recorder
	// Events receives shipping.rates.changed.
	Events eventbus.Publisher
	// Stores gives the store's currency and country.
	Stores StoreSource
	Cache  CacheConfig
	// Carriers configures the adapters merchants may connect (api and
	// worker roles); the storefront role leaves it empty.
	Carriers config.Shipping
	// Sealer seals carrier credentials; nil in roles that never connect
	// a carrier (the storefront).
	Sealer *envelope.Sealer
	// PublicURL is the platform's public origin, for webhook URLs.
	PublicURL string
	// Orders reads and moves the orders shipments carry (api, worker).
	Orders Orders
	// Jobs enqueues shipment jobs in the caller's transaction (api, worker).
	Jobs jobs.Inserter
	// Objects stores shipping labels (api, worker).
	Objects media.Objects
	// Inbox gives webhook jobs their receipts (worker).
	Inbox webhooks.Inbox
}

// carriers builds the adapters c offers, and the fake one's courier.
func carriers(c config.Shipping) (app.Carriers, *fake.Carrier) {
	out := app.Carriers{bosta.Name: bosta.New(bosta.Options{BaseURL: c.BostaBaseURL})}
	if !c.FakeCarrier {
		return out, nil
	}
	f := fake.New()
	out[fake.Name] = f
	return out, f
}

// courier adapts the fake carrier to FakeCourier.
type courier struct{ f *fake.Carrier }

func (c courier) Advance(tracking, status string) error {
	return c.f.Advance(tracking, carrier.Status(status))
}

func (c courier) Webhook(tracking string) ([]byte, error) { return c.f.Webhook(tracking) }

// Module is the wired shipping module.
type Module struct {
	svc   *app.Service
	fake  *fake.Carrier
	redis *redis.Client
	log   *slog.Logger
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Redis == nil || d.Log == nil || d.Events == nil || d.Stores == nil {
		return nil, errors.New("shipping: missing dependency")
	}
	if d.Cache.Stores == 0 {
		d.Cache = app.DefaultCacheConfig()
	}
	cs, f := carriers(d.Carriers)
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events, Stores: d.Stores, Cache: d.Cache,
		Carriers: cs, Sealer: d.Sealer, PublicURL: d.PublicURL,
		Orders: d.Orders, Jobs: d.Jobs, Objects: d.Objects, Inbox: d.Inbox}),
		fake: f, redis: d.Redis, log: d.Log}, nil
}

// FakeCourier moves the fake carrier's parcels; nil unless the fake
// carrier is enabled (never in production).
func (m *Module) FakeCourier() FakeCourier {
	if m.fake == nil {
		return nil
	}
	return courier{f: m.fake}
}

// Quoter is what orders use to price deliveries.
func (m *Module) Quoter() Quoter { return m.svc }

// RegisterWorkers adds the shipment jobs' workers (worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// WebhookProviders are the carriers' sides of the webhooks inbox, one per
// carrier; register them on the webhooks module.
func (m *Module) WebhookProviders() []webhooks.Provider { return m.svc.WebhookProviders() }

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// Listen applies invalidations published on InvalidateChannel until ctx
// ends. A lost message costs at most the cache TTL.
func (m *Module) Listen(ctx context.Context) {
	for payload := range redisx.Subscribe(ctx, m.redis, InvalidateChannel, m.log) {
		id, err := tenant.ParseStoreID(payload)
		if err != nil {
			m.log.WarnContext(ctx, "shipping: bad invalidation message", "err", err)
			continue
		}
		m.svc.Invalidate(id)
	}
}
