package risk

import (
	"context"
	"errors"
	"net/http"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool postgres.Beginner
	// Orders reads the store's orders (orders.Module.Outcomes).
	Orders orders.Outcomes
	// PhoneKey is RISK_PHONE_KEY (at least 32 bytes).
	PhoneKey []byte
	// Jobs queues each store's backfill (api and worker roles).
	Jobs jobs.Inserter
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
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Orders: d.Orders, Keyer: keyer, Jobs: d.Jobs})}, nil
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

// orderEvent is what risk reads of orders.EventOrderPlaced and
// orders.EventOrderStatusChanged: which order.
type orderEvent struct {
	event.Meta
	OrderID ulid.ID `json:"order_id"`
}

// LearnHandler learns from an order whenever it is placed or moves.
// Subscribe it as LearnSubscriber to orders.EventOrderPlaced and
// orders.EventOrderStatusChanged (worker role). An order gone meanwhile
// teaches nothing.
func (m *Module) LearnHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orderEvent) error {
		if err := m.svc.Learn(ctx, ev.OrderID); err != nil && !errors.Is(err, app.ErrOrderNotFound) {
			return err
		}
		return nil
	})
}
