package entitlements

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	// Source is where grants come from: FromBilling in production, Fixed
	// in tests and demo tools.
	Source Source
	// IDs and Events publish UsageCrossed; nil Events discards.
	IDs    *ulid.Generator
	Events eventbus.Publisher
}

// Module is the wired entitlements module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Source == nil {
		return nil, errors.New("entitlements: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Source: d.Source, IDs: d.IDs, Events: d.Events})}, nil
}

// Entitlements is what other modules check and meter with.
func (m *Module) Entitlements() Entitlements { return m.svc }

// Invalidate drops this instance's cached grants of store, after its
// plan changed.
func (m *Module) Invalidate(store tenant.StoreID) { m.svc.Invalidate(store) }

// InvalidateChannel is the Redis channel on which instances learn that a
// store's plan changed.
const InvalidateChannel = "entitlements:invalidate"

// InvalidateSubscriber is the event bus subscriber name of
// InvalidateHandler.
const InvalidateSubscriber = "entitlements.invalidate_grants"

// InvalidateHandler tells every instance that the event's store's plan
// changed. Subscribe it to billing.EventSubscriptionChanged on every
// role's bus (ADR-0050).
func InvalidateHandler(rdb *redis.Client) eventbus.Handler {
	return func(ctx context.Context, d eventbus.Delivery) error {
		return rdb.Publish(ctx, InvalidateChannel, d.Meta.StoreID.String()).Err()
	}
}

// Listen applies invalidations published on InvalidateChannel until ctx
// ends. A lost message costs at most the cache TTL (30 s).
func (m *Module) Listen(ctx context.Context, rdb *redis.Client, log *slog.Logger) {
	for payload := range redisx.Subscribe(ctx, rdb, InvalidateChannel, log) {
		id, err := tenant.ParseStoreID(payload)
		if err != nil {
			log.WarnContext(ctx, "entitlements: bad invalidation message", "err", err)
			continue
		}
		m.svc.Invalidate(id)
	}
}

// FromBilling reads grants from the store's plan. Features the code does
// not enforce (keys the console added for display) are left out.
func FromBilling(b billing.GrantSource) Source { return billingSource{b} }

type billingSource struct{ b billing.GrantSource }

func (s billingSource) GrantsTx(ctx context.Context, tx pgx.Tx) (Grants, error) {
	in, err := s.b.GrantsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make(Grants, len(in))
	for key, g := range in {
		if f := domain.Feature(key); f.Kind() != "" {
			out[f] = Grant{Value: g.Value, Unlimited: g.Unlimited}
		}
	}
	return out, nil
}
