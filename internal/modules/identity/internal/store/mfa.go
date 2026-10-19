package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store/identitydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors for unique conflicts.
var (
	ErrPhoneTaken       = errors.New("identity store: phone taken")
	ErrInvitationExists = errors.New("identity store: open invitation exists")
	ErrAlreadyMember    = errors.New("identity store: already a member")
)

// UserByPhone finds the account with this verified phone.
func UserByPhone(ctx context.Context, db Tx, e164 string) (domain.User, error) {
	row, err := identitydb.New(db).GetUserByPhone(ctx, &e164)
	if err != nil {
		return domain.User{}, noRows(err)
	}
	return toUser(row), nil
}

// SetVerifiedPhone stores a phone the user proved they control.
func SetVerifiedPhone(ctx context.Context, db Tx, userID ulid.ID, e164 string) error {
	err := identitydb.New(db).SetVerifiedPhone(ctx, identitydb.SetVerifiedPhoneParams{ID: userID, Phone: &e164})
	if postgres.IsUniqueViolation(err, "users_phone_key") {
		return ErrPhoneTaken
	}
	if err != nil {
		return fmt.Errorf("identity store: set phone: %w", err)
	}
	return nil
}

// SetPendingTOTP stores an unconfirmed sealed seed. It reports false when
// 2FA is already enabled.
func SetPendingTOTP(ctx context.Context, db Tx, userID ulid.ID, sealed []byte) (bool, error) {
	n, err := identitydb.New(db).SetPendingTOTP(ctx, identitydb.SetPendingTOTPParams{ID: userID, TotpSecret: sealed})
	if err != nil {
		return false, fmt.Errorf("identity store: pending totp: %w", err)
	}
	return n == 1, nil
}

// EnableTOTP turns 2FA on, recording the confirming step, and stores the
// recovery code hashes, in tx.
func EnableTOTP(ctx context.Context, tx Tx, userID ulid.ID, step int64, codeHashes [][]byte) (bool, error) {
	q := identitydb.New(tx)
	n, err := q.EnableTOTP(ctx, identitydb.EnableTOTPParams{ID: userID, Step: step})
	if err != nil {
		return false, fmt.Errorf("identity store: enable totp: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return false, fmt.Errorf("identity store: recovery codes: %w", err)
	}
	rows := make([]identitydb.InsertRecoveryCodesParams, len(codeHashes))
	for i, h := range codeHashes {
		rows[i] = identitydb.InsertRecoveryCodesParams{UserID: userID, CodeHash: h}
	}
	if _, err := q.InsertRecoveryCodes(ctx, rows); err != nil {
		return false, fmt.Errorf("identity store: recovery codes: %w", err)
	}
	return true, nil
}

// DisableTOTP removes the seed and recovery codes, in tx.
func DisableTOTP(ctx context.Context, tx Tx, userID ulid.ID) error {
	q := identitydb.New(tx)
	if err := q.DisableTOTP(ctx, userID); err != nil {
		return fmt.Errorf("identity store: disable totp: %w", err)
	}
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("identity store: recovery codes: %w", err)
	}
	return nil
}

// UseTOTPStep claims a time step; false means it (or a later one) was
// already used: a replay.
func UseTOTPStep(ctx context.Context, db Tx, userID ulid.ID, step int64) (bool, error) {
	n, err := identitydb.New(db).UseTOTPStep(ctx, identitydb.UseTOTPStepParams{ID: userID, Step: step})
	if err != nil {
		return false, fmt.Errorf("identity store: totp step: %w", err)
	}
	return n == 1, nil
}

// UseRecoveryCode consumes a recovery code; false if unknown or used.
func UseRecoveryCode(ctx context.Context, db Tx, userID ulid.ID, hash []byte) (bool, error) {
	n, err := identitydb.New(db).UseRecoveryCode(ctx, identitydb.UseRecoveryCodeParams{UserID: userID, CodeHash: hash})
	if err != nil {
		return false, fmt.Errorf("identity store: recovery code: %w", err)
	}
	return n == 1, nil
}

// TouchDevice records a sign-in device and reports whether it is new and
// whether the user had other devices before it.
func TouchDevice(ctx context.Context, tx Tx, userID ulid.ID, deviceHash []byte, ua, ipPrefix string) (isNew, hadOthers bool, err error) {
	q := identitydb.New(tx)
	inserted, err := q.UpsertDevice(ctx, identitydb.UpsertDeviceParams{UserID: userID, DeviceHash: deviceHash, UserAgent: ua, IpPrefix: ipPrefix})
	if err != nil {
		return false, false, fmt.Errorf("identity store: device: %w", err)
	}
	if !inserted {
		return false, true, nil
	}
	n, err := q.CountDevices(ctx, userID)
	if err != nil {
		return false, false, fmt.Errorf("identity store: devices: %w", err)
	}
	return true, n > 1, nil
}

// Invitation is a pending invitation to the current store.
type Invitation struct {
	ID         ulid.ID
	StoreID    tenant.StoreID
	Email      string
	Role       authz.Role
	InvitedBy  ulid.ID
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	RevokedAt  *time.Time
}

func toInvitation(r identitydb.StoreInvitation) Invitation {
	return Invitation{ID: r.ID, StoreID: tenant.StoreID{ID: r.StoreID}, Email: r.Email, Role: authz.Role(r.Role),
		InvitedBy: r.InvitedBy, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, AcceptedAt: r.AcceptedAt, RevokedAt: r.RevokedAt}
}

// NewInvitation is an invitation row to insert.
type NewInvitation struct {
	ID        ulid.ID
	Email     string
	Role      authz.Role
	TokenHash []byte
	InvitedBy ulid.ID
	ExpiresAt time.Time
}

// InsertInvitation adds an invitation to the current store, refusing an
// email that is already a member or already has an open invitation.
func InsertInvitation(ctx context.Context, tx Tx, in NewInvitation) (Invitation, error) {
	store, err := current(ctx)
	if err != nil {
		return Invitation{}, err
	}
	q := identitydb.New(tx)
	member, err := q.IsMemberByEmail(ctx, identitydb.IsMemberByEmailParams{StoreID: store, Email: in.Email})
	if err != nil {
		return Invitation{}, fmt.Errorf("identity store: member check: %w", err)
	}
	if member {
		return Invitation{}, ErrAlreadyMember
	}
	row, err := q.InsertInvitation(ctx, identitydb.InsertInvitationParams{
		ID: in.ID, StoreID: store, Email: in.Email, Role: string(in.Role), TokenHash: in.TokenHash,
		InvitedBy: in.InvitedBy, ExpiresAt: in.ExpiresAt,
	})
	if postgres.IsUniqueViolation(err, "store_invitations_one_open") {
		return Invitation{}, ErrInvitationExists
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("identity store: insert invitation: %w", err)
	}
	return toInvitation(row), nil
}

// ListInvitations returns the current store's open invitations.
func ListInvitations(ctx context.Context, tx Tx) ([]Invitation, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := identitydb.New(tx).ListOpenInvitations(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("identity store: invitations: %w", err)
	}
	out := make([]Invitation, len(rows))
	for i, r := range rows {
		out[i] = toInvitation(r)
	}
	return out, nil
}

// GetInvitation returns one of the current store's invitations.
func GetInvitation(ctx context.Context, tx Tx, id ulid.ID) (Invitation, error) {
	store, err := current(ctx)
	if err != nil {
		return Invitation{}, err
	}
	row, err := identitydb.New(tx).GetInvitation(ctx, identitydb.GetInvitationParams{StoreID: store, ID: id})
	if err != nil {
		return Invitation{}, noRows(err)
	}
	return toInvitation(row), nil
}

// OpenInvitationByToken finds a usable invitation of the current store.
func OpenInvitationByToken(ctx context.Context, tx Tx, tokenHash []byte) (Invitation, error) {
	store, err := current(ctx)
	if err != nil {
		return Invitation{}, err
	}
	row, err := identitydb.New(tx).GetOpenInvitationByToken(ctx, identitydb.GetOpenInvitationByTokenParams{StoreID: store, TokenHash: tokenHash})
	if err != nil {
		return Invitation{}, noRows(err)
	}
	return toInvitation(row), nil
}

// RevokeInvitation cancels an open invitation; false if none.
func RevokeInvitation(ctx context.Context, tx Tx, id ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := identitydb.New(tx).RevokeInvitation(ctx, identitydb.RevokeInvitationParams{StoreID: store, ID: id})
	if err != nil {
		return false, fmt.Errorf("identity store: revoke invitation: %w", err)
	}
	return n == 1, nil
}

// MarkInvitationAccepted closes an invitation; false if it was not open.
func MarkInvitationAccepted(ctx context.Context, tx Tx, id ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := identitydb.New(tx).AcceptInvitation(ctx, identitydb.AcceptInvitationParams{StoreID: store, ID: id})
	if err != nil {
		return false, fmt.Errorf("identity store: accept invitation: %w", err)
	}
	return n == 1, nil
}

// Member is a store team member.
type Member struct {
	UserID   ulid.ID
	Name     string
	Email    string
	Role     authz.Role
	JoinedAt time.Time
}

// ListMembers returns the current store's team in one query.
func ListMembers(ctx context.Context, tx Tx) ([]Member, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := identitydb.New(tx).ListMembers(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("identity store: members: %w", err)
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{UserID: r.UserID, Name: r.Name, Email: r.Email, Role: authz.Role(r.Role), JoinedAt: r.CreatedAt}
	}
	return out, nil
}

// MemberRole returns a user's role in the current store.
func MemberRole(ctx context.Context, tx Tx, userID ulid.ID) (authz.Role, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	role, err := identitydb.New(tx).GetMemberRole(ctx, identitydb.GetMemberRoleParams{StoreID: store, UserID: userID})
	if err != nil {
		return "", noRows(err)
	}
	return authz.Role(role), nil
}

// AddMember inserts a membership, reporting ErrAlreadyMember on conflict.
func AddMember(ctx context.Context, tx Tx, userID ulid.ID, role authz.Role) error {
	err := InsertMembership(ctx, tx, userID, role)
	if err != nil && postgres.IsUniqueViolation(errors.Unwrap(err), "store_memberships_pkey") {
		return ErrAlreadyMember
	}
	return err
}

// SetMemberRole changes a non-owner's role; false if no such non-owner.
func SetMemberRole(ctx context.Context, tx Tx, userID ulid.ID, role authz.Role) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := identitydb.New(tx).UpdateMemberRole(ctx, identitydb.UpdateMemberRoleParams{StoreID: store, UserID: userID, Role: string(role)})
	if err != nil {
		return false, fmt.Errorf("identity store: set role: %w", err)
	}
	return n == 1, nil
}

// RemoveMember deletes a non-owner membership; false if none.
func RemoveMember(ctx context.Context, tx Tx, userID ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := identitydb.New(tx).DeleteMember(ctx, identitydb.DeleteMemberParams{StoreID: store, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("identity store: remove member: %w", err)
	}
	return n == 1, nil
}
