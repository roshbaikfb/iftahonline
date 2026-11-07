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

// SupportTeam is a store's team for the console (C1): members, pending
// invitations, and support requests open or closed lately.
type SupportTeam = app.SupportTeam

// SupportMember is a store member as the console sees them (the phone
// masked).
type SupportMember = app.SupportMember

// SupportRequest is a delayed change platform support asked for (C1).
type SupportRequest = store.SupportRequest

// MemberStore is a store a user works on, with the role.
type MemberStore = store.MemberStore

// Support request kinds.
const (
	SupportMFAReset          = store.KindMFAReset
	SupportOwnershipTransfer = store.KindOwnershipTransfer
)

// MemberStores lists the stores (at most 20) user is a member of, on db,
// the console's pool (C1): IDs and roles only.
func MemberStores(ctx context.Context, db store.Tx, user ulid.ID) ([]MemberStore, error) {
	return store.MemberStores(ctx, db, user)
}

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

// OwnerTx is the store-in-ctx's owner, read in its tenant transaction tx
// (billing's founding program admits one store per owner).
func OwnerTx(ctx context.Context, tx pgx.Tx) (ulid.ID, error) { return store.Owner(ctx, tx) }

// OwnerContact is how to reach a store's owner (the console's store
// page): shown to admins, never logged.
type OwnerContact = store.OwnerContact

// OwnerContactTx reads the store-in-ctx's owner in its tenant
// transaction tx, in one query; ok is false for a store without one.
func OwnerContactTx(ctx context.Context, tx pgx.Tx) (OwnerContact, bool, error) {
	return store.ReadOwnerContact(ctx, tx)
}

// OwnerStores lists the stores (at most 20) whose owner has exactly this
// email (normalized) or one of these E.164 phones, on db, the console's
// pool: store IDs only, never the contact (ADR-0079).
func OwnerStores(ctx context.Context, db store.Tx, email string, phones []string) ([]ulid.ID, error) {
	return store.OwnerStores(ctx, db, email, phones)
}

// CountStaff counts the store-in-ctx's team seats (members and pending
// invitations) in its tenant transaction tx: the staff usage meter
// (ADR-0061), the count the plan's limit admits against.
func CountStaff(ctx context.Context, tx pgx.Tx) (int64, error) { return store.CountSeats(ctx, tx) }
