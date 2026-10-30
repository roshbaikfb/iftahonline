package tenancy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool   *pgxpool.Pool
	Redis  *redis.Client
	Jobs   jobs.Inserter
	IDs    *ulid.Generator
	Log    *slog.Logger
	Config config.Tenancy
	// DNS resolves TXT records for domain verification; nil uses a
	// pure-Go net.Resolver.
	DNS app.TXTResolver
	// Audit records store actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// AdminPool is the app_admin pool for the super-admin directory;
	// nil means no directory (every role but the api).
	AdminPool *pgxpool.Pool
	// Plan admits custom domains within the store's plan (ADR-0045); nil
	// admits every one (tests).
	Plan entitlements.Entitlements
}

// Module is the wired tenancy module.
type Module struct {
	svc      *app.Service
	resolver *app.Resolver
	cache    *store.HostCache
	deps     Deps
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Redis == nil || d.Jobs == nil || d.IDs == nil || d.Log == nil {
		return nil, errors.New("tenancy: missing dependency")
	}
	if d.DNS == nil {
		d.DNS = &net.Resolver{PreferGo: true}
	}
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	c := d.Config
	cache := store.NewHostCache(d.Redis, c.SharedCacheTTL, c.NegativeTTL)
	resolver, err := app.NewResolver(app.ResolverConfig{
		Size: c.HostCacheSize, TTL: c.HostCacheTTL,
		NegSize: c.NegativeCacheSize, NegTTL: c.NegativeTTL,
		LoadTimeout: 2 * time.Second,
	}, cache, store.Directory{DB: d.Pool}, d.Log)
	if err != nil {
		return nil, err
	}
	return &Module{
		svc:      app.NewService(d.Pool, d.Jobs, d.IDs, c.BaseDomain, d.Audit, d.Plan),
		resolver: resolver,
		cache:    cache,
		deps:     d,
	}, nil
}

// Service returns the store use cases.
func (m *Module) Service() Service { return m.svc }

// Directory returns the cross-tenant store directory, or nil when the
// module has no admin pool.
func (m *Module) Directory() Directory {
	if m.deps.AdminPool == nil {
		return nil
	}
	return directoryAdapter{app.NewStoreDirectory(m.deps.AdminPool)}
}

// directoryAdapter maps the app's page type onto the api's.
type directoryAdapter struct{ d *app.StoreDirectory }

func (a directoryAdapter) Search(ctx context.Context, query, status, cursor string, limit int) (DirectoryPage, error) {
	p, err := a.d.Search(ctx, query, status, cursor, limit)
	if err != nil {
		return DirectoryPage{}, err
	}
	out := DirectoryPage{NextCursor: p.NextCursor, Items: make([]DirectoryEntry, len(p.Items))}
	for i, e := range p.Items {
		out.Items[i] = DirectoryEntry{Store: e.Store, PrimaryHost: e.PrimaryHost}
	}
	return out, nil
}

func (a directoryAdapter) Store(ctx context.Context, id tenant.StoreID) (Store, []Domain, error) {
	return a.d.Store(ctx, id)
}

// Resolver returns the host resolver.
func (m *Module) Resolver() HostResolver { return m.resolver }

// ListenInvalidations evicts hosts from this instance's cache as changes
// are published, until ctx ends. Storefront instances run it.
func (m *Module) ListenInvalidations(ctx context.Context) {
	m.resolver.Listen(ctx, m.cache.Subscribe(ctx, m.deps.Log))
}

// RegisterWorkers adds the module's job workers.
func (m *Module) RegisterWorkers(w *river.Workers) error {
	if err := river.AddWorkerSafely(w, &app.VerifyWorker{
		DB: m.deps.Pool, DNS: m.deps.DNS, Svc: m.svc, LookupTimeout: 5 * time.Second,
	}); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	if err := river.AddWorkerSafely(w, &app.InvalidateWorker{Cache: m.cache}); err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	return nil
}

// Mount registers the HTTP API. scope is identity's membership middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	c := m.deps.Config
	edge := domain.Edge{Target: c.DomainTarget, IPv4: c.DomainIPv4, IPv6: c.DomainIPv6}
	httpapi.Mount(mux, m.svc, edge, auth, scope, resp)
}
