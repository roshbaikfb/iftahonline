package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The team page (ADR-0078): admins, their invites and resets.

// InTx runs fn in one transaction of db, the console's pool.
func InTx(ctx context.Context, db DB, fn func(DB) error) error {
	b, ok := db.(postgres.Beginner)
	if !ok {
		return errors.New("admin store: no transactions on this connection")
	}
	return postgres.InTx(ctx, b, func(tx pgx.Tx) error { return fn(tx) })
}

// Member is an admin as the team page shows it: no secrets.
type Member struct {
	ID          ulid.ID
	Name, Email string
	Role        domain.Role
	Active      bool
	Resetting   bool // a reset link waits for the admin
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// Team lists every admin, the active first.
func Team(ctx context.Context, db DB) ([]Member, error) {
	rows, err := admindb.New(db).ListTeam(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: team: %w", err)
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{ID: r.ID, Name: r.Name, Email: r.Email, Role: domain.Role(r.Role), Active: r.Status == "active", Resetting: r.Resetting,
			CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt}
	}
	return out, nil
}

// Invite is a one-time link to join the console or to reset an admin.
type Invite struct {
	ID, AdminID   ulid.ID
	Purpose       string // join or reset
	Email, Name   string
	Role          domain.Role
	TOTPSecret    []byte // sealed for AdminID
	InvitedBy     ulid.ID
	InvitedByName string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	Used          bool
	Cancelled     bool
}

// OpenInvites lists the new admins invited and not joined yet, newest
// first (at most 200).
func OpenInvites(ctx context.Context, db DB) ([]Invite, error) {
	rows, err := admindb.New(db).ListOpenInvites(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: invites: %w", err)
	}
	out := make([]Invite, len(rows))
	for i, r := range rows {
		out[i] = Invite{ID: r.ID, AdminID: r.AdminID, Purpose: "join", Email: r.Email, Name: r.Name, Role: domain.Role(r.Role),
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, InvitedByName: r.InvitedByName}
	}
	return out, nil
}

// InsertInvite records an invite under its token's hash, closing any
// open one of the same admin first.
func InsertInvite(ctx context.Context, db DB, inv Invite, tokenHash []byte) error {
	q := admindb.New(db)
	if _, err := q.CancelOpenInvites(ctx, inv.AdminID); err != nil {
		return fmt.Errorf("admin store: cancel invites: %w", err)
	}
	err := q.InsertInvite(ctx, admindb.InsertInviteParams{ID: inv.ID, TokenHash: tokenHash, Purpose: inv.Purpose, AdminID: inv.AdminID,
		Email: inv.Email, Name: inv.Name, Role: string(inv.Role), TotpSecret: inv.TOTPSecret, InvitedBy: inv.InvitedBy, ExpiresAt: inv.ExpiresAt})
	if postgres.IsUniqueViolation(err, "admin_invites_open_email") {
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("admin store: insert invite: %w", err)
	}
	return nil
}

// OpenInvite is invite id while it is open; domain.ErrNotFound otherwise.
func OpenInvite(ctx context.Context, db DB, id ulid.ID) (Invite, error) {
	r, err := admindb.New(db).GetOpenInvite(ctx, id)
	if err != nil {
		return Invite{}, noRows(err)
	}
	return Invite{ID: r.ID, AdminID: r.AdminID, Purpose: r.Purpose, Email: r.Email, Name: r.Name, Role: domain.Role(r.Role),
		TOTPSecret: r.TotpSecret, InvitedBy: r.InvitedBy, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}, nil
}

// CancelInvite closes admin's open invite; false when none was open.
func CancelInvite(ctx context.Context, db DB, admin ulid.ID) (bool, error) {
	n, err := admindb.New(db).CancelOpenInvites(ctx, admin)
	if err != nil {
		return false, fmt.Errorf("admin store: cancel invite: %w", err)
	}
	return n > 0, nil
}

// InviteByToken finds an invite by its link's hash, in any state.
func InviteByToken(ctx context.Context, db DB, hash []byte) (Invite, error) {
	r, err := admindb.New(db).GetInviteByToken(ctx, hash)
	if err != nil {
		return Invite{}, noRows(err)
	}
	return Invite{ID: r.ID, AdminID: r.AdminID, Purpose: r.Purpose, Email: r.Email, Name: r.Name, Role: domain.Role(r.Role),
		TOTPSecret: r.TotpSecret, InvitedBy: r.InvitedBy, InvitedByName: r.InvitedByName, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		Used: r.UsedAt != nil, Cancelled: r.CancelledAt != nil}, nil
}

// UseInvite spends invite id; false when it was spent, closed or expired
// meanwhile.
func UseInvite(ctx context.Context, db DB, id ulid.ID) (bool, error) {
	n, err := admindb.New(db).UseInvite(ctx, id)
	if err != nil {
		return false, fmt.Errorf("admin store: use invite: %w", err)
	}
	return n == 1, nil
}

// InsertInvitedAdmin creates the admin an invite was for.
func InsertInvitedAdmin(ctx context.Context, db DB, inv Invite, hash string) error {
	err := admindb.New(db).InsertInvitedAdmin(ctx, admindb.InsertInvitedAdminParams{ID: inv.AdminID, Email: inv.Email, Name: inv.Name,
		PasswordHash: hash, TotpSecret: inv.TOTPSecret, Role: string(inv.Role)})
	if postgres.IsUniqueViolation(err, "") {
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("admin store: insert invited admin: %w", err)
	}
	return nil
}

// ResetCredentials gives admin id a new password and TOTP seed and
// enables it.
func ResetCredentials(ctx context.Context, db DB, id ulid.ID, hash string, sealedTOTP []byte) error {
	n, err := admindb.New(db).ResetAdminCredentials(ctx, admindb.ResetAdminCredentialsParams{ID: id, PasswordHash: hash, TotpSecret: sealedTOTP})
	if err != nil {
		return fmt.Errorf("admin store: reset credentials: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// LockOwners locks the active owners for the rest of tx.
func LockOwners(ctx context.Context, db DB) ([]ulid.ID, error) {
	ids, err := admindb.New(db).LockOwners(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: lock owners: %w", err)
	}
	return ids, nil
}

// SetRole changes admin id's role.
func SetRole(ctx context.Context, db DB, id ulid.ID, role domain.Role) error {
	n, err := admindb.New(db).SetAdminRole(ctx, admindb.SetAdminRoleParams{ID: id, Role: string(role)})
	if err != nil {
		return fmt.Errorf("admin store: set role: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetActive enables or disables admin id; disabling ends its sessions.
func SetActive(ctx context.Context, db DB, id ulid.ID, active bool) error {
	status := "disabled"
	if active {
		status = "active"
	}
	q := admindb.New(db)
	n, err := q.SetAdminStatus(ctx, admindb.SetAdminStatusParams{ID: id, Status: status})
	if err != nil {
		return fmt.Errorf("admin store: set status: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	if !active {
		if _, err = q.RevokeAdminSessions(ctx, id); err != nil {
			return fmt.Errorf("admin store: revoke sessions: %w", err)
		}
	}
	return nil
}
