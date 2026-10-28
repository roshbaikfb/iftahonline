package webhooks

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool    *pgxpool.Pool
	IDs     *ulid.Generator
	Limiter *ratelimit.Limiter
	Log     *slog.Logger
	// ClientIP is the server's RealIP lookup (middleware.ClientIP).
	ClientIP httpapi.ClientIPFunc
	// Limits overrides DefaultLimits (tests).
	Limits *Limits
}

// Module is the wired webhooks module.
type Module struct {
	svc      *app.Service
	clientIP httpapi.ClientIPFunc
}

// New wires the module. Providers are added with Register at startup.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Limiter == nil || d.Log == nil || d.ClientIP == nil {
		return nil, errors.New("webhooks: missing dependency")
	}
	lim := app.DefaultLimits()
	if d.Limits != nil {
		lim = *d.Limits
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Limiter: d.Limiter, Limits: lim, Log: d.Log}),
		clientIP: d.ClientIP}, nil
}

// Register adds a provider; its deliveries arrive at
// /v1/webhooks/<name>/<store>/<connection>. Names are unique.
func (m *Module) Register(p Provider) error { return m.svc.Register(p) }

// RegisterPlatform adds a provider's platform-owned account; its
// deliveries arrive at /v1/webhooks/<name>. Names are unique.
func (m *Module) RegisterPlatform(p PlatformProvider) error { return m.svc.RegisterPlatform(p) }

// Inbox is what the providers' processing jobs use.
func (m *Module) Inbox() Inbox { return m.svc }

// Mount registers the public webhook route (no session).
func (m *Module) Mount(mux *http.ServeMux, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, resp, m.clientIP)
}
