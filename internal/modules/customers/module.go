package customers

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	IDs  *ulid.Generator
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Region is the store's phone region (from its currency).
	Region RegionSource
}

// Module is the wired customers module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Region == nil {
		return nil, errors.New("customers: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Region: d.Region})}, nil
}

// Customers is the API other modules (orders) use.
func (m *Module) Customers() Customers { return m.svc }

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}
