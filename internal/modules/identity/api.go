// Package identity owns accounts, sessions, store memberships and the
// audit log (ADR-0005, ADR-0006). This file and module.go are the
// module's only exported surface; see docs/modules/identity.md.
//
// Other modules never import identity: they receive an authz.Authorizer,
// the Scope middleware and an audit.Recorder through their constructors.
package identity

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// NewAuditRecorder returns the audit log writer to give other modules.
// It needs nothing but an ID generator, so it can be built before the
// module itself.
func NewAuditRecorder(ids *ulid.Generator) audit.Recorder { return app.NewRecorder(ids) }
