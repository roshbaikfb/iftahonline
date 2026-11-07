package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store/identitydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Platform support on a store's team (C1): support_requests is a tenant
// table (the store from ctx); the cancel links are user_tokens, which are
// platform-wide and name their request's store.

// Support request kinds.
const (
	KindMFAReset          = "mfa_reset"
	KindOwnershipTransfer = "ownership_transfer"
	PurposeSupportCancel  = "support_cancel"
)

// Who cancelled a request.
const (
	CancelledByLink    = "link"
	CancelledByConsole = "console"
)

// TeamMember is a member as the console sees them.
type TeamMember struct {
	UserID     ulid.ID
	Name       string
	Email      string
	Phone      string // E.164 or empty; masked before it leaves identity
	Role       authz.Role
	JoinedAt   time.Time
	MFAEnabled bool
	Disabled   bool
	LastSeenAt time.Time // zero: never
}

// ListTeam returns the current store's members for the console, in one
// query.
func ListTeam(ctx context.Context, tx Tx) ([]TeamMember, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := identitydb.New(tx).ListTeam(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("identity store: team: %w", err)
	}
	out := make([]TeamMember, len(rows))
	for i, r := range rows {
		out[i] = TeamMember{UserID: r.UserID, Name: r.Name, Email: r.Email, Phone: r.Phone, Role: authz.Role(r.Role), JoinedAt: r.CreatedAt,
			MFAEnabled: r.MfaEnabled, Disabled: r.Status == string(domain.UserDisabled), LastSeenAt: r.LastSeenAt}
	}
	return out, nil
}

// MemberStore is a store a user works on.
type MemberStore struct {
	StoreID ulid.ID
	Role    authz.Role
}

// MemberStores lists the stores (at most 20) userID is a member of, on
// db, the console's pool.
func MemberStores(ctx context.Context, db Tx, userID ulid.ID) ([]MemberStore, error) {
	rows, err := identitydb.New(db).MemberStores(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity store: member stores: %w", err)
	}
	out := make([]MemberStore, len(rows))
	for i, r := range rows {
		out[i] = MemberStore{StoreID: r.StoreID, Role: authz.Role(r.Role)}
	}
	return out, nil
}

// SetUserStatus sets an account active or disabled; false when it
// already was.
func SetUserStatus(ctx context.Context, tx Tx, userID ulid.ID, status domain.UserStatus) (bool, error) {
	n, err := identitydb.New(tx).SetUserStatus(ctx, identitydb.SetUserStatusParams{ID: userID, Status: string(status)})
	if err != nil {
		return false, fmt.Errorf("identity store: user status: %w", err)
	}
	return n == 1, nil
}

// SupportRequest is a delayed change platform support asked for.
type SupportRequest struct {
	ID          ulid.ID
	Kind        string
	UserID      ulid.ID
	FromUserID  *ulid.ID
	RequestedBy ulid.ID
	Reason      string
	CreatedAt   time.Time
	EffectiveAt time.Time
	CancelledAt *time.Time
	CancelledBy string
	DoneAt      *time.Time
}

// Open reports whether the request still waits.
func (r SupportRequest) Open() bool { return r.CancelledAt == nil && r.DoneAt == nil }

func toRequest(r identitydb.SupportRequest) SupportRequest {
	out := SupportRequest{ID: r.ID, Kind: r.Kind, UserID: r.UserID, FromUserID: r.FromUserID, RequestedBy: r.RequestedBy, Reason: r.Reason,
		CreatedAt: r.CreatedAt, EffectiveAt: r.EffectiveAt, CancelledAt: r.CancelledAt, DoneAt: r.DoneAt}
	if r.CancelledBy != nil {
		out.CancelledBy = *r.CancelledBy
	}
	return out
}

// ErrRequestOpen means the same request already waits.
var ErrRequestOpen = errors.New("identity store: a request is already open")

// InsertSupportRequest records a request in the current store;
// ErrRequestOpen when one of its kind already waits for the member (or,
// for a transfer, for the store).
func InsertSupportRequest(ctx context.Context, tx Tx, r SupportRequest) (SupportRequest, error) {
	store, err := current(ctx)
	if err != nil {
		return SupportRequest{}, err
	}
	row, err := identitydb.New(tx).InsertSupportRequest(ctx, identitydb.InsertSupportRequestParams{ID: r.ID, StoreID: store, Kind: r.Kind,
		UserID: r.UserID, FromUserID: r.FromUserID, RequestedBy: r.RequestedBy, Reason: r.Reason, EffectiveAt: r.EffectiveAt})
	if postgres.IsUniqueViolation(err, "") {
		return SupportRequest{}, ErrRequestOpen
	}
	if err != nil {
		return SupportRequest{}, fmt.Errorf("identity store: insert request: %w", err)
	}
	return toRequest(row), nil
}

// GetSupportRequest reads a request of the current store, locked for the
// transaction when lock; domain.ErrNotFound when it is not there.
func GetSupportRequest(ctx context.Context, tx Tx, id ulid.ID, lock bool) (SupportRequest, error) {
	store, err := current(ctx)
	if err != nil {
		return SupportRequest{}, err
	}
	q := identitydb.New(tx)
	var row identitydb.SupportRequest
	if lock {
		row, err = q.LockSupportRequest(ctx, identitydb.LockSupportRequestParams{StoreID: store, ID: id})
	} else {
		row, err = q.GetSupportRequest(ctx, identitydb.GetSupportRequestParams{StoreID: store, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportRequest{}, domain.ErrNotFound
	}
	if err != nil {
		return SupportRequest{}, fmt.Errorf("identity store: request: %w", err)
	}
	return toRequest(row), nil
}

// ListSupportRequests returns the current store's open requests and those
// closed since since, newest first.
func ListSupportRequests(ctx context.Context, tx Tx, since time.Time) ([]SupportRequest, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := identitydb.New(tx).ListSupportRequests(ctx, identitydb.ListSupportRequestsParams{StoreID: store, Since: &since})
	if err != nil {
		return nil, fmt.Errorf("identity store: requests: %w", err)
	}
	out := make([]SupportRequest, len(rows))
	for i, r := range rows {
		out[i] = toRequest(r)
	}
	return out, nil
}

// CloseSupportRequest cancels (by is who) or finishes (by empty) an open
// request of the current store and closes its cancel links; false when
// it was not open.
func CloseSupportRequest(ctx context.Context, tx Tx, id ulid.ID, by string) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	q := identitydb.New(tx)
	var n int64
	if by == "" {
		n, err = q.FinishSupportRequest(ctx, identitydb.FinishSupportRequestParams{StoreID: store, ID: id})
	} else {
		n, err = q.CancelSupportRequest(ctx, identitydb.CancelSupportRequestParams{StoreID: store, ID: id, CancelledBy: &by})
	}
	if err != nil {
		return false, fmt.Errorf("identity store: close request: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if err = q.CloseSupportTokens(ctx, &id); err != nil {
		return false, fmt.Errorf("identity store: close request links: %w", err)
	}
	return true, nil
}

// InsertSupportToken records a request's cancel link for one recipient
// in the current store: only its hash; it works until the request takes
// effect.
func InsertSupportToken(ctx context.Context, tx Tx, id, userID, requestID ulid.ID, email string, hash []byte, expires time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = identitydb.New(tx).InsertSupportToken(ctx, identitydb.InsertSupportTokenParams{ID: id, UserID: userID, TokenHash: hash, Email: email,
		ExpiresAt: expires, RequestID: &requestID, LinkStoreID: &store}); err != nil {
		return fmt.Errorf("identity store: insert request link: %w", err)
	}
	return nil
}

// SupportLink is what an open cancel link names.
type SupportLink struct {
	RequestID ulid.ID
	StoreID   ulid.ID
	UserID    ulid.ID // whom the link was sent to: the member, or the owner
	Email     string
}

// PeekSupportToken reads an open cancel link without using it (any store:
// the link says which); domain.ErrNotFound when it is unknown, used or
// expired.
func PeekSupportToken(ctx context.Context, db Tx, hash []byte) (SupportLink, error) {
	r, err := identitydb.New(db).PeekSupportToken(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportLink{}, domain.ErrNotFound
	}
	if err != nil {
		return SupportLink{}, fmt.Errorf("identity store: check request link: %w", err)
	}
	return SupportLink{RequestID: r.RequestID, StoreID: r.StoreID, UserID: r.UserID, Email: r.Email}, nil
}

// SupportTokenToSend is an open cancel link its email job sends.
type SupportTokenToSend struct {
	UserID    ulid.ID
	Email     string
	Name      string
	RequestID ulid.ID
}

// SupportTokenForSend reads an open cancel link; domain.ErrNotFound when
// its request closed meanwhile.
func SupportTokenForSend(ctx context.Context, db Tx, id ulid.ID) (SupportTokenToSend, error) {
	r, err := identitydb.New(db).SupportTokenForSend(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportTokenToSend{}, domain.ErrNotFound
	}
	if err != nil {
		return SupportTokenToSend{}, fmt.Errorf("identity store: request link to send: %w", err)
	}
	return SupportTokenToSend{UserID: r.UserID, Email: r.Email, Name: r.Name, RequestID: r.RequestID}, nil
}

// TransferOwner moves the current store's ownership from from (who becomes
// a manager) to to, in tx; false when from is not the owner or to is not
// a member.
func TransferOwner(ctx context.Context, tx Tx, from, to ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	q := identitydb.New(tx)
	n, err := q.DemoteOwner(ctx, identitydb.DemoteOwnerParams{StoreID: store, UserID: from})
	if err != nil || n == 0 {
		return false, wrapErr("demote owner", err)
	}
	n, err = q.PromoteToOwner(ctx, identitydb.PromoteToOwnerParams{StoreID: store, UserID: to})
	if err != nil || n == 0 {
		return false, wrapErr("promote owner", err)
	}
	return true, nil
}

func wrapErr(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("identity store: %s: %w", what, err)
}
