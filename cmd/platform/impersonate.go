package main

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// impersonator lets the console open support sessions through identity
// (ADR-0055); neither module imports the other.
func impersonator(id *identity.Module) admin.Impersonator {
	return func(ctx context.Context, by ulid.ID, reason string, d time.Duration, write bool) (admin.Impersonation, error) {
		g, err := id.Impersonate(ctx, by, reason, d, write)
		return admin.Impersonation{Code: g.Code, CodeExpiresAt: g.CodeExpiresAt, ExpiresAt: g.ExpiresAt}, err
	}
}
