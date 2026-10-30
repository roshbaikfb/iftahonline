package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's dependencies. Redis, Directory, Stores and
// AdminHost are needed to serve the console; the CLI needs only the rest.
type Deps struct {
	AdminPool *pgxpool.Pool // app_admin
	Redis     *redis.Client
	Directory tenancy.Directory
	Stores    tenancy.Service
	IDs       *ulid.Generator
	Log       *slog.Logger
	Secrets   config.Secrets
	Hashing   config.Identity
	AdminHost string
	ClientIP  httpapi.ClientIPFunc
	// Impersonate opens support sessions (identity, ADR-0055) at
	// DashboardURL; nil refuses them.
	Impersonate  Impersonator
	DashboardURL string
	Limits       *app.Limits // tests
}

// Module is the wired console.
type Module struct {
	svc  *app.Service
	deps Deps
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.AdminPool == nil || d.IDs == nil || d.Log == nil || d.Secrets.Keyring == nil {
		return nil, errors.New("admin: missing dependency")
	}
	hasher, err := password.New(password.Params{MemoryKiB: d.Hashing.Argon2MemoryKiB, Time: d.Hashing.Argon2Time, Threads: d.Hashing.Argon2Threads},
		max(d.Hashing.HashConcurrency, 1))
	if err != nil {
		return nil, err
	}
	var limiter *ratelimit.Limiter
	if d.Redis != nil {
		if limiter, err = ratelimit.New(d.Redis, "admin"); err != nil {
			return nil, err
		}
	}
	limits := app.DefaultLimits()
	if d.Limits != nil {
		limits = *d.Limits
	}
	svc := app.NewService(app.Deps{DB: d.AdminPool, Hasher: hasher, Limiter: limiter, Limits: limits, Keyring: d.Secrets.Keyring,
		IDs: d.IDs, Log: d.Log, Directory: d.Directory, Stores: d.Stores, Impersonate: d.Impersonate, DashboardURL: d.DashboardURL})
	return &Module{svc: svc, deps: d}, nil
}

// CreateAdmin adds a platform admin (the `platform admin create` CLI).
func (m *Module) CreateAdmin(ctx context.Context, email, name, password string) (Created, error) {
	return m.svc.CreateAdmin(ctx, email, name, password)
}

// Mount registers the console routes; they answer only on AdminHost.
func (m *Module) Mount(mux *http.ServeMux, resp *problem.Responder) error {
	d := m.deps
	if d.Redis == nil || d.Directory == nil || d.Stores == nil || d.AdminHost == "" {
		return errors.New("admin: serving the console needs Redis, the store directory, the tenancy service and the admin host")
	}
	httpapi.Mount(mux, m.svc, httpapi.Scope(m.svc, resp, d.AdminHost, d.ClientIP), resp)
	return nil
}
