package storefront

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
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
	// Events receives the module's domain events (ADR-0015).
	Events eventbus.Publisher
}

// Module is the wired storefront module.
type Module struct {
	svc      *app.Service
	sections *sections.Registry
}

// New wires the module. It fails if a section schema does not compile.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Events == nil {
		return nil, errors.New("storefront: missing dependency")
	}
	reg, err := sections.New()
	if err != nil {
		return nil, err
	}
	return &Module{
		svc:      app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events, Sections: reg}),
		sections: reg,
	}, nil
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, m.sections, auth, scope, resp)
}
