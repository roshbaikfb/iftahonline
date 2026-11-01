package analytics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4). The
// api role gives Read (the dashboard); the worker gives Orders, Charges
// and Jobs (learning and computing).
type Deps struct {
	Pool    postgres.Beginner
	Read    *postgres.ReadPool
	Orders  orders.Outcomes
	Charges shipping.Charges
	Stores  StoreSource
	Jobs    jobs.Inserter
	Plan    entitlements.Entitlements
	Audit   audit.Recorder
	IDs     *ulid.Generator
}

// Module is the wired analytics module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Stores == nil || d.IDs == nil {
		return nil, errors.New("analytics: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Read: d.Read, Orders: d.Orders, Charges: d.Charges, Stores: d.Stores,
		Jobs: d.Jobs, Plan: d.Plan, Audit: d.Audit, IDs: d.IDs, Now: time.Now})}, nil
}

// Mount registers the merchant API (the api role, with Read).
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// RegisterWorkers registers the backfill, rollup and sweep jobs (worker
// role, with Orders, Charges and Jobs).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// PeriodicJobs are the module's schedule.
func (*Module) PeriodicJobs() []*river.PeriodicJob { return app.Periodic() }

// orderEvent is what analytics reads of the orders module's events:
// which order.
type orderEvent struct {
	event.Meta
	OrderID ulid.ID `json:"order_id"`
}

// LearnHandler keeps an order's facts whenever it is placed, moves or
// is paid. Subscribe it as LearnSubscriber (worker role).
func (m *Module) LearnHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orderEvent) error { return m.svc.Learn(ctx, ev.OrderID) })
}
