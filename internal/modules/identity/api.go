// Package identity owns accounts, sessions, store memberships and the
// audit log (ADR-0005, ADR-0006). This file and module.go are the
// module's only exported surface; see docs/modules/identity.md.
//
// Other modules never import identity: they receive an authz.Authorizer,
// the Scope middleware and an audit.Recorder through their constructors.
package identity

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ImpersonationGrant is a support session's one-time code (ADR-0055).
type ImpersonationGrant = app.ImpersonationGrant

// NewAuditRecorder returns the audit log writer to give other modules.
// It needs nothing but an ID generator, so it can be built before the
// module itself.
func NewAuditRecorder(ids *ulid.Generator) audit.Recorder { return app.NewRecorder(ids) }

// StaffPhones returns a function listing the verified phones (E.164) of
// the store-in-ctx's members with one of roles, for the platform's
// WhatsApp notices to them (billing, ADR-0054). It reads in one query.
func StaffPhones(db postgres.Beginner, roles ...authz.Role) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) (out []string, err error) {
		err = postgres.InTenantTx(ctx, db, func(tx pgx.Tx) (txErr error) {
			out, txErr = store.MemberPhones(ctx, tx, roles)
			return txErr
		})
		return out, err
	}
}

// StaffEmails returns a function listing the email addresses of the
// store-in-ctx's members with one of roles, for notices other modules
// email to the store's staff. It reads in one query.
func StaffEmails(db postgres.Beginner, roles ...authz.Role) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		var out []string
		err := postgres.InTenantTx(ctx, db, func(tx pgx.Tx) error {
			members, err := store.ListMembers(ctx, tx)
			for _, m := range members {
				if slices.Contains(roles, m.Role) {
					out = append(out, m.Email)
				}
			}
			return err
		})
		return out, err
	}
}

// CountStaff counts the store-in-ctx's team seats (members and pending
// invitations) in its tenant transaction tx: the staff usage meter
// (ADR-0061), the count the plan's limit admits against.
func CountStaff(ctx context.Context, tx pgx.Tx) (int64, error) { return store.CountSeats(ctx, tx) }
