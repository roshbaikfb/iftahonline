package billing

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
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
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool})}, nil
}

// Grants is what the entitlements module reads.
func (m *Module) Grants() GrantSource { return m.svc }
