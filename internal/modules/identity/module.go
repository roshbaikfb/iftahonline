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
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/notify"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool    *pgxpool.Pool
	Redis   *redis.Client
	Tenancy tenancy.Service
	// Onboarding opens each new store's setup wizard at sign-up.
	Onboarding onboarding.Starter
	IDs        *ulid.Generator
	Log        *slog.Logger
	Config     config.Identity
	// ClientIP is the server's RealIP lookup (middleware.ClientIP).
	ClientIP httpapi.ClientIPFunc
	// Limits overrides DefaultLimits (tests).
	Limits  *app.Limits
	Secrets config.Secrets
	Jobs    jobs.Inserter
	// Env decides whether phone OTP is available (see OTPAvailable).
	Env string
}

// OTPAvailable reports whether phone codes can be delivered: outside
// production they go to Mailpit as email; in production they need an
// SMS/WhatsApp provider, which is not chosen yet (ADR-0005).
func OTPAvailable(env string) bool { return env != config.EnvProduction }

// Module is the wired identity module.
type Module struct {
	svc    *app.Service
	policy authz.Policy
	scope  func(http.Handler) http.Handler
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Redis == nil || d.Tenancy == nil || d.Onboarding == nil || d.IDs == nil || d.Log == nil ||
		d.Jobs == nil || d.Secrets.Keyring == nil || len(d.Secrets.OTPPepper) < 32 {
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
	svc := app.NewService(app.Deps{
		DB: d.Pool, Hasher: hasher, Limiter: limiter, Limits: limits, Stores: d.Tenancy, Onboarding: d.Onboarding, IDs: d.IDs, Log: d.Log,
		Redis: d.Redis, Keyring: d.Secrets.Keyring, Pepper: d.Secrets.OTPPepper, Jobs: d.Jobs, OTPEnabled: OTPAvailable(d.Env),
	})
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

// WorkerDeps are what the identity jobs need in the worker role (no
// password hasher, no HTTP).
type WorkerDeps struct {
	Pool    *pgxpool.Pool
	Redis   *redis.Client
	Secrets config.Secrets
	Mail    config.Mail
	Env     string
	Tenancy tenancy.Service
	Log     *slog.Logger
}

// RegisterWorkers adds the module's job workers: audit partitions, phone
// code delivery, invitation email and new-device alerts.
func RegisterWorkers(w *river.Workers, d WorkerDeps) error {
	if d.Pool == nil || d.Redis == nil || d.Secrets.Keyring == nil || d.Tenancy == nil || d.Log == nil {
		return errors.New("identity: missing worker dependency")
	}
	var mailer notify.Mailer = notify.LogMailer{Log: d.Log}
	if d.Mail.SMTPAddr != "" {
		mailer = notify.SMTPMailer{Addr: d.Mail.SMTPAddr, Username: d.Mail.SMTPUsername, Password: d.Mail.SMTPPassword, From: d.Mail.From}
	}
	var otp notify.OTPSender = notify.DisabledOTPSender{}
	if OTPAvailable(d.Env) {
		otp = notify.MailOTPSender{Mailer: mailer}
	}
	return app.RegisterWorkers(w, app.WorkerDeps{
		DB: d.Pool, Redis: d.Redis, Keyring: d.Secrets.Keyring, Mailer: mailer, OTP: otp,
		Stores: d.Tenancy, DashboardURL: d.Mail.DashboardURL, Log: d.Log,
	})
}
