package promotions

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	IDs  *ulid.Generator
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Plan says whether the store's plan includes coupons (ADR-0046); nil
	// includes them (tests).
	Plan entitlements.Entitlements
}

// Module is the wired promotions module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil {
		return nil, errors.New("promotions: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Plan: d.Plan})}, nil
}

// Coupons is what orders use.
func (m *Module) Coupons() Coupons { return m.svc }

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// beforeShipping are the statuses an order is cancelled from before it
// ships: its coupon's use comes back. A parcel that was sent cost the
// merchant; its use stays.
func beforeShipping(s orders.Status) bool {
	return s == orders.StatusPending || s == orders.StatusConfirmed || s == orders.StatusReadyToShip
}

// OrderStatusHandler gives back the coupon's use of an order cancelled
// before it shipped. Subscribe it as ReleaseCouponsSubscriber to
// orders.EventOrderStatusChanged (worker role).
func (m *Module) OrderStatusHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderStatusChanged) error {
		if ev.To != orders.StatusCancelled || !beforeShipping(ev.From) {
			return nil
		}
		return m.svc.ReleaseOrder(ctx, ev.OrderID)
	})
}
