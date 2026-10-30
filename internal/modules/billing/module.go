package billing

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	// Orders reads delivered and returned orders for their fees (the
	// worker); nil elsewhere.
	Orders Orders
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
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Orders: d.Orders})}, nil
}

// Grants is what the entitlements module reads.
func (m *Module) Grants() GrantSource { return m.svc }

// Fees records pay-as-you-sell fees (the worker; needs Deps.Orders).
func (m *Module) Fees() Fees { return m.svc }
