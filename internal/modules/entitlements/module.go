package entitlements

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool   *pgxpool.Pool
	Config config.Entitlements
}

// Module is the wired entitlements module.
type Module struct {
	svc *app.Service
}

// New wires the module with the interim limits from the environment.
func New(d Deps) (*Module, error) {
	if d.Pool == nil {
		return nil, errors.New("entitlements: missing dependency")
	}
	limits := app.StaticLimits{Messages: d.Config.MessagesPerMonth}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, Limits: limits})}, nil
}

// Entitlements is what metered modules use.
func (m *Module) Entitlements() Entitlements { return m.svc }
