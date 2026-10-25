package onboarding

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	// Jobs enqueues the apply job in the choice's transaction.
	Jobs jobs.Inserter
	// Templates applies starter templates (the worker); nil applies
	// nothing, which is all the api role needs.
	Templates Templates
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
}

// Module is the wired onboarding module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Jobs == nil {
		return nil, errors.New("onboarding: missing dependency")
	}
	if d.Templates == nil {
		d.Templates = noTemplates{}
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Jobs: d.Jobs, Templates: d.Templates, Audit: d.Audit})}, nil
}

// noTemplates applies nothing. The api role only enqueues apply jobs;
// the worker runs them with the real templates.
type noTemplates struct{}

func (noTemplates) Apply(context.Context, Choice) (int, error) { return 0, nil }

// Starter is what sign-up uses.
func (m *Module) Starter() Starter { return m.svc }

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// RegisterWorkers adds the module's job workers (the worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }
