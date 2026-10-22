package orders

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/httpapi"
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
	// Audit records order actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Events receives the module's domain events (ADR-0015).
	Events eventbus.Publisher
	// Catalog prices orders and holds, ships and restocks their stock.
	Catalog catalog.Catalog
	// Customers records who ordered and refuses blocked ones.
	Customers customers.Customers
	// Stores gives the store's currency, country and time zone.
	Stores StoreSource
	// Auth checks each transition's own permission.
	Auth authz.Authorizer
}

// Module is the wired orders module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Events == nil || d.Catalog == nil || d.Customers == nil || d.Stores == nil || d.Auth == nil {
		return nil, errors.New("orders: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events,
		Catalog: d.Catalog, Customers: d.Customers, Stores: d.Stores, Auth: d.Auth})}, nil
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}
