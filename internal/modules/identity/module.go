package identity

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/password"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool    *pgxpool.Pool
	Redis   *redis.Client
	Tenancy tenancy.Service
	IDs     *ulid.Generator
	Log     *slog.Logger
	Config  config.Identity
	// ClientIP is the server's RealIP lookup (middleware.ClientIP).
	ClientIP httpapi.ClientIPFunc
	// Limits overrides DefaultLimits (tests).
	Limits *app.Limits
}

// Module is the wired identity module.
type Module struct {
	svc    *app.Service
	policy authz.Policy
	scope  func(http.Handler) http.Handler
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Redis == nil || d.Tenancy == nil || d.IDs == nil || d.Log == nil {
		return nil, errors.New("identity: missing dependency")
	}
	hasher, err := password.New(password.Params{
		MemoryKiB: d.Config.Argon2MemoryKiB, Time: d.Config.Argon2Time, Threads: d.Config.Argon2Threads,
	}, d.Config.HashConcurrency)
	if err != nil {
		return nil, err
	}
	limiter, err := ratelimit.New(d.Redis, "identity")
	if err != nil {
		return nil, err
	}
	limits := app.DefaultLimits()
	if d.Limits != nil {
		limits = *d.Limits
	}
	policy, err := domain.NewPolicy()
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	svc := app.NewService(d.Pool, hasher, limiter, limits, d.Tenancy, d.IDs, d.Log)
	return &Module{svc: svc, policy: policy, scope: httpapi.Scope(svc, problem.NewResponder(d.Log), d.ClientIP)}, nil
}

// Authorizer is the role × permission policy every handler checks.
func (m *Module) Authorizer() authz.Authorizer { return m.policy }

// Scope is the authentication/membership middleware for API routes.
func (m *Module) Scope() func(http.Handler) http.Handler { return m.scope }

// Mount registers the identity HTTP API.
func (m *Module) Mount(mux *http.ServeMux, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, m.policy, m.scope, resp)
}

// RegisterWorkers adds the module's job workers. It needs only the pool,
// so the worker role does not build the whole module (no hasher).
func RegisterWorkers(w *river.Workers, pool *pgxpool.Pool) error {
	if err := river.AddWorkerSafely(w, &app.AuditPartitionsWorker{DB: pool}); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	return nil
}

// PeriodicJobs are the module's scheduled jobs.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{app.AuditPartitionsPeriodic()}
}
