package billing

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/gateway/paymob"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
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
	// Events receives SubscriptionChanged (the worker); nil discards.
	Events eventbus.Publisher
	// Gateways, Sealer, Holds and Notifier collect invoices and run
	// dunning (the worker, ADR-0051); Collect turns the hourly run on.
	// Paymob adds the paymob gateway from the platform's accounts
	// (ADR-0052).
	Paymob   config.Billing
	Gateways map[string]Gateway
	Sealer   Sealer
	Holds    Holds
	Notifier Notifier
	Collect  bool
}

// Module is the wired billing module.
type Module struct {
	svc     *app.Service
	collect bool
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: missing dependency")
	}
	gateways := map[string]Gateway{}
	for name, g := range d.Gateways {
		gateways[name] = g
	}
	if len(d.Paymob.Paymob) > 0 {
		regions := make(map[string]paymob.Region, len(d.Paymob.Paymob))
		for cur, a := range d.Paymob.Paymob {
			regions[cur] = paymob.Region{SecretKey: a.SecretKey, APIKey: a.APIKey, MotoIntegrationID: a.MotoIntegrationID}
		}
		gateways[paymob.Name] = paymob.New(paymob.Options{Regions: regions, BaseURL: d.Paymob.PaymobBaseURL})
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Orders: d.Orders, Stores: d.Stores, Jobs: d.Jobs, IDs: d.IDs, Events: d.Events,
		Gateways: gateways, Sealer: d.Sealer, Holds: d.Holds, Notifier: d.Notifier}), collect: d.Collect}, nil
}

// Grants is what the entitlements module reads.
func (m *Module) Grants() GrantSource { return m.svc }

// Fees records pay-as-you-sell fees (the worker; needs Deps.Orders).
func (m *Module) Fees() Fees { return m.svc }

// RegisterWorkers adds the monthly invoice jobs (the worker; needs
// Deps.Stores and Deps.Jobs).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// Collections records payments from outside the card on file.
func (m *Module) Collections() Collections { return m.svc }

// PeriodicJobs start each month's invoices (daily), end trials and
// cancelled periods (hourly) and, with Deps.Collect, run the dunning
// (hourly); all at worker start.
func (m *Module) PeriodicJobs() []*river.PeriodicJob {
	jobs := []*river.PeriodicJob{app.InvoicesPeriodic(), app.EndPeriodsPeriodic()}
	if m.collect {
		jobs = append(jobs, app.DunningPeriodic())
	}
	return jobs
}
