package billing

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	// Orders reads delivered and returned orders for their fees, Stores
	// names stores on invoices, Jobs enqueues the invoice jobs, IDs makes
	// accounts' and invoices' IDs (the worker); nil elsewhere.
	Orders Orders
	Stores Stores
	Jobs   jobs.Inserter
	IDs    *ulid.Generator
}

// Module is the wired billing module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: missing dependency")
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Orders: d.Orders, Stores: d.Stores, Jobs: d.Jobs, IDs: d.IDs})}, nil
}

// Grants is what the entitlements module reads.
func (m *Module) Grants() GrantSource { return m.svc }

// Fees records pay-as-you-sell fees (the worker; needs Deps.Orders).
func (m *Module) Fees() Fees { return m.svc }

// RegisterWorkers adds the monthly invoice jobs (the worker; needs
// Deps.Stores and Deps.Jobs).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// PeriodicJobs start each month's invoices: daily, and at worker start.
func (m *Module) PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{app.InvoicesPeriodic()}
}
