package catalog

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool     *pgxpool.Pool
	Jobs     jobs.Inserter
	IDs      *ulid.Generator
	Log      *slog.Logger
	Currency CurrencySource
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
}

// Module is the wired catalog module.
type Module struct {
	svc  *app.Service
	deps Deps
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Jobs == nil || d.IDs == nil || d.Log == nil || d.Currency == nil {
		return nil, errors.New("catalog: missing dependency")
	}
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	return &Module{svc: app.NewService(d.Pool, d.Jobs, d.IDs, d.Audit, d.Currency), deps: d}, nil
}

// Catalog returns the use cases other modules call.
func (m *Module) Catalog() Catalog { return m.svc }

// RegisterWorkers adds the module's job workers.
func (m *Module) RegisterWorkers(w *river.Workers) error {
	if err := river.AddWorkerSafely(w, &app.ExpireWorker{DB: m.deps.Pool}); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	return nil
}

// Mount registers the HTTP API. scope is identity's membership middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}
