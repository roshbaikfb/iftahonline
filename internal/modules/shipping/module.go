package shipping

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/bosta"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
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
}

// carriers builds the adapters c offers.
func carriers(c config.Shipping) app.Carriers {
	out := app.Carriers{bosta.Name: bosta.New(bosta.Options{BaseURL: c.BostaBaseURL})}
	if c.FakeCarrier {
		out[fake.Name] = fake.New()
	}
	return out
}

// Module is the wired shipping module.
type Module struct {
	svc   *app.Service
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
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events, Stores: d.Stores, Cache: d.Cache,
		Carriers: carriers(d.Carriers), Sealer: d.Sealer, PublicURL: d.PublicURL}),
		redis: d.Redis, log: d.Log}, nil
}

// Quoter is what orders use to price deliveries.
func (m *Module) Quoter() Quoter { return m.svc }

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
