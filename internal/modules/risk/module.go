package risk

import (
	"context"
	"errors"
	"net/http"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool postgres.Beginner
	// Orders reads the store's orders (orders.Module.Outcomes).
	Orders orders.Outcomes
	// PhoneKey is RISK_PHONE_KEY (at least 32 bytes).
	PhoneKey []byte
	// Jobs queues each store's backfill.
	Jobs jobs.Inserter
	// Plan says whether the store's plan has the rules (ADR-0066).
	Plan entitlements.Entitlements
	// Audit records rule changes (identity's audit log); nil discards.
	Audit audit.Recorder
}

// Module is the wired risk module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Orders == nil || d.Jobs == nil {
		return nil, errors.New("risk: missing dependency")
	}
	keyer, err := domain.NewKeyer(d.PhoneKey)
	if err != nil {
		return nil, err
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Orders: d.Orders, Keyer: keyer, Jobs: d.Jobs, Plan: d.Plan, Audit: d.Audit})}, nil
}

// CheckerDeps are what the rules need at placement: no orders, no jobs,
// so the orders module can be built with them (orders cannot wait for
// the module that reads it).
type CheckerDeps struct {
	Pool     postgres.Beginner
	PhoneKey []byte
	Plan     entitlements.Entitlements
}

// NewChecker builds what orders ask before placing a storefront cash
// order (every role that places orders).
func NewChecker(d CheckerDeps) (Checker, error) {
	if d.Pool == nil {
		return nil, errors.New("risk: missing dependency")
	}
	keyer, err := domain.NewKeyer(d.PhoneKey)
	if err != nil {
		return nil, err
	}
	return app.NewService(app.Deps{DB: d.Pool, Keyer: keyer, Plan: d.Plan}), nil
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// RegisterWorkers registers the backfill and the purge (worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// PeriodicJobs are the module's scheduled jobs: the daily purge.
func (*Module) PeriodicJobs() []*river.PeriodicJob { return []*river.PeriodicJob{app.PurgePeriodic()} }

// LearnHandler learns from an order whenever it is placed or moves.
// Subscribe it as LearnSubscriber to orders.EventOrderPlaced and
// orders.EventOrderStatusChanged (worker role). An order gone meanwhile
// teaches nothing.
func (m *Module) LearnHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderRef) error {
		if err := m.svc.Learn(ctx, ev.OrderID); err != nil && !errors.Is(err, app.ErrOrderNotFound) {
			return err
		}
		return nil
	})
}
