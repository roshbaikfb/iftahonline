package orders

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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
	// Shipping quotes each order's delivery rate and COD fee.
	Shipping shipping.Quoter
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
	if d.Pool == nil || d.IDs == nil || d.Events == nil || d.Catalog == nil || d.Customers == nil || d.Shipping == nil ||
		d.Stores == nil || d.Auth == nil {
		return nil, errors.New("orders: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events,
		Catalog: d.Catalog, Customers: d.Customers, Shipping: d.Shipping, Stores: d.Stores, Auth: d.Auth})}, nil
}

// Fulfilment is the API courier integrations use.
func (m *Module) Fulfilment() Fulfilment { return m.svc }

// Payments is the API payment integrations use.
func (m *Module) Payments() Payments { return m.svc }

// Shop is the API shoppers use (through the storefront).
func (m *Module) Shop() Shop { return m.svc }

// ShopDeps are what the storefront role needs to place orders: no
// merchant permissions, since shoppers never move orders.
type ShopDeps struct {
	Pool      *pgxpool.Pool
	IDs       *ulid.Generator
	Audit     audit.Recorder
	Events    eventbus.Publisher
	Catalog   catalog.Catalog
	Customers customers.Customers
	Shipping  shipping.Quoter
	Stores    StoreSource
}

// NewShop builds the shopper's side for the storefront role. Merchant
// actions (transitions) are refused: it holds no authorizer.
func NewShop(d ShopDeps) (Shop, error) {
	svc, err := withoutPermissions(d)
	if err != nil {
		return nil, err // never a typed nil in the interface
	}
	return svc, nil
}

// NewFulfilment builds what the worker role's courier and payment jobs
// need: read orders, move them and record their payments on a system's
// behalf. It takes the shopper side's
// dependencies and holds no merchant permissions either.
func NewFulfilment(d ShopDeps) (Payments, error) {
	svc, err := withoutPermissions(d)
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// withoutPermissions builds the service for roles that act for shoppers
// or systems, never for merchants: every permission is refused.
func withoutPermissions(d ShopDeps) (*app.Service, error) {
	if d.Pool == nil || d.IDs == nil || d.Events == nil || d.Catalog == nil || d.Customers == nil || d.Shipping == nil || d.Stores == nil {
		return nil, errors.New("orders: missing dependency")
	}
	return app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events,
		Catalog: d.Catalog, Customers: d.Customers, Shipping: d.Shipping, Stores: d.Stores, Auth: denyAll{}}), nil
}

// denyAll refuses every permission.
type denyAll struct{}

func (denyAll) Authorize(context.Context, authz.Permission) error {
	return errs.Forbidden("shoppers cannot do this")
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}
