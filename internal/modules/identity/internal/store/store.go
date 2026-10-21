// Package store adapts the identity sqlc queries to domain types.
//
// users and user_sessions are platform-wide; store_memberships and
// audit_log are tenant tables, and the functions on them take the store
// from tenant.FromContext (CLAUDE.md rule 5).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store/identitydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is a transaction (or pool, for platform-wide reads).
type Tx = identitydb.DBTX

// ErrEmailTaken means another account uses the email.
var ErrEmailTaken = errors.New("identity store: email taken")

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("identity store: %w", err)
	}
	return id.ID, nil
}

func noRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// InsertUser creates an account.
func InsertUser(ctx context.Context, tx Tx, id ulid.ID, email, name, hash string) (domain.User, error) {
	row, err := identitydb.New(tx).InsertUser(ctx, identitydb.InsertUserParams{ID: id, Email: email, Name: name, PasswordHash: hash})
	if err != nil {
		if postgres.IsUniqueViolation(err, "users_email_key") {
			return domain.User{}, ErrEmailTaken
		}
		return domain.User{}, fmt.Errorf("identity store: insert user: %w", err)
	}
	return toUser(row), nil
}

// UserByEmail finds an account by normalized email.
func UserByEmail(ctx context.Context, db Tx, email string) (domain.User, error) {
	row, err := identitydb.New(db).GetUserByEmail(ctx, email)
	if err != nil {
		return domain.User{}, noRows(err)
	}
	return toUser(row), nil
}

// UserByID finds an account.
func UserByID(ctx context.Context, db Tx, id ulid.ID) (domain.User, error) {
	row, err := identitydb.New(db).GetUser(ctx, id)
	if err != nil {
		return domain.User{}, noRows(err)
	}
	return toUser(row), nil
}

// SetPasswordHash replaces a user's hash (parameter upgrades, resets).
func SetPasswordHash(ctx context.Context, db Tx, id ulid.ID, hash string) error {
	if err := identitydb.New(db).UpdatePasswordHash(ctx, identitydb.UpdatePasswordHashParams{ID: id, PasswordHash: hash}); err != nil {
		return fmt.Errorf("identity store: update hash: %w", err)
	}
	return nil
}

func toUser(r identitydb.User) domain.User {
	u := domain.User{
		ID: r.ID, Email: r.Email, EmailVerifiedAt: r.EmailVerifiedAt, Name: r.Name, PasswordHash: r.PasswordHash,
		Status: domain.UserStatus(r.Status), TOTPSecret: r.TotpSecret, TOTPEnabledAt: r.TotpEnabledAt,
		TOTPLastStep: r.TotpLastStep, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Phone != nil {
		u.Phone = *r.Phone
	}
	return u
}

// NewSession is a session row to insert.
type NewSession struct {
	ID        ulid.ID
	UserID    ulid.ID
	Token     authtoken.Token
	UserAgent string
	IPPrefix  string
	ExpiresAt time.Time
}

// InsertSession stores a session (its token's hash only).
func InsertSession(ctx context.Context, tx Tx, s NewSession) (domain.Session, error) {
	row, err := identitydb.New(tx).InsertSession(ctx, identitydb.InsertSessionParams{
		ID: s.ID, UserID: s.UserID, TokenHash: s.Token.Hash(), UserAgent: s.UserAgent, IpPrefix: s.IPPrefix, ExpiresAt: s.ExpiresAt,
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("identity store: insert session: %w", err)
	}
	return toSession(row), nil
}

// ActiveSession is a live session with its user's status and, in a store
// context, the user's role there ("" for a non-member).
type ActiveSession struct {
	ID, UserID          ulid.ID
	CreatedAt, LastSeen time.Time
	ExpiresAt           time.Time
	UserStatus          domain.UserStatus
	Role                authz.Role
}

// SessionByToken loads a live session.
func SessionByToken(ctx context.Context, db Tx, t authtoken.Token) (ActiveSession, error) {
	r, err := identitydb.New(db).GetActiveSession(ctx, t.Hash())
	if err != nil {
		return ActiveSession{}, noRows(err)
	}
	return ActiveSession{ID: r.ID, UserID: r.UserID, CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
		UserStatus: domain.UserStatus(r.UserStatus)}, nil
}

// SessionByTokenInStore loads a live session and the user's role in the
// current store, in one query.
func SessionByTokenInStore(ctx context.Context, tx Tx, t authtoken.Token) (ActiveSession, error) {
	store, err := current(ctx)
	if err != nil {
		return ActiveSession{}, err
	}
	r, err := identitydb.New(tx).GetActiveSessionInStore(ctx, identitydb.GetActiveSessionInStoreParams{TokenHash: t.Hash(), StoreID: store})
	if err != nil {
		return ActiveSession{}, noRows(err)
	}
	return ActiveSession{ID: r.ID, UserID: r.UserID, CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
		UserStatus: domain.UserStatus(r.UserStatus), Role: authz.Role(r.Role)}, nil
}

// TouchSession records activity and slides the expiry.
func TouchSession(ctx context.Context, db Tx, id ulid.ID, expires time.Time) error {
	if err := identitydb.New(db).TouchSession(ctx, identitydb.TouchSessionParams{ID: id, ExpiresAt: expires}); err != nil {
		return fmt.Errorf("identity store: touch session: %w", err)
	}
	return nil
}

// RevokeSession ends one of the user's sessions. It reports whether a live
// session of that user was found.
func RevokeSession(ctx context.Context, db Tx, userID, sessionID ulid.ID) (bool, error) {
	n, err := identitydb.New(db).RevokeSession(ctx, identitydb.RevokeSessionParams{ID: sessionID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("identity store: revoke session: %w", err)
	}
	return n == 1, nil
}

// ListSessions returns the user's live sessions, newest first.
func ListSessions(ctx context.Context, db Tx, userID ulid.ID) ([]domain.Session, error) {
	rows, err := identitydb.New(db).ListUserSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity store: list sessions: %w", err)
	}
	out := make([]domain.Session, len(rows))
	for i, r := range rows {
		out[i] = toSession(r)
	}
	return out, nil
}

func toSession(r identitydb.UserSession) domain.Session {
	return domain.Session{ID: r.ID, UserID: r.UserID, UserAgent: r.UserAgent, IPPrefix: r.IpPrefix,
		CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt}
}

// Membership is a user's role in a store.
type Membership struct {
	StoreID   tenant.StoreID
	Role      authz.Role
	CreatedAt time.Time
}

// InsertMembership adds the user to the current store.
func InsertMembership(ctx context.Context, tx Tx, userID ulid.ID, role authz.Role) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if _, err := identitydb.New(tx).InsertMembership(ctx, identitydb.InsertMembershipParams{StoreID: store, UserID: userID, Role: string(role)}); err != nil {
		return fmt.Errorf("identity store: insert membership: %w", err)
	}
	return nil
}

// MyMemberships lists the user's memberships across stores. tx must be
// user-scoped (SetUserScope first); RLS then returns only their rows.
func MyMemberships(ctx context.Context, tx Tx, userID ulid.ID) ([]Membership, error) {
	q := identitydb.New(tx)
	if err := q.SetUserScope(ctx, userID); err != nil {
		return nil, fmt.Errorf("identity store: user scope: %w", err)
	}
	rows, err := q.ListMyMemberships(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity store: memberships: %w", err)
	}
	out := make([]Membership, len(rows))
	for i, r := range rows {
		out[i] = Membership{StoreID: tenant.StoreID{ID: r.StoreID}, Role: authz.Role(r.Role), CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// AuditEntry is a stored audit record.
type AuditEntry struct {
	ID          ulid.ID
	OccurredAt  time.Time
	ActorUserID *ulid.ID
	Action      string
	TargetType  string
	TargetID    string
	IPPrefix    string
	Metadata    []byte
}

// InsertAudit appends an entry for the current store.
func InsertAudit(ctx context.Context, tx Tx, e AuditEntry) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	meta := e.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	if err := identitydb.New(tx).InsertAuditEntry(ctx, identitydb.InsertAuditEntryParams{
		ID: e.ID, StoreID: store, ActorUserID: e.ActorUserID, Action: e.Action,
		TargetType: e.TargetType, TargetID: e.TargetID, IpPrefix: e.IPPrefix, Metadata: meta,
	}); err != nil {
		return fmt.Errorf("identity store: insert audit: %w", err)
	}
	return nil
}

// ListAudit pages the current store's log, newest first, strictly before
// (beforeAt, beforeID) when beforeAt is set.
func ListAudit(ctx context.Context, tx Tx, beforeAt *time.Time, beforeID *ulid.ID, limit int) ([]AuditEntry, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := identitydb.New(tx).ListAuditEntries(ctx, identitydb.ListAuditEntriesParams{
		StoreID: store, BeforeAt: beforeAt, BeforeID: beforeID, Lim: int32(limit), //nolint:gosec // limit is bounded by the caller.
	})
	if err != nil {
		return nil, fmt.Errorf("identity store: list audit: %w", err)
	}
	out := make([]AuditEntry, len(rows))
	for i, r := range rows {
		out[i] = AuditEntry{ID: r.ID, OccurredAt: r.OccurredAt, ActorUserID: r.ActorUserID, Action: r.Action,
			TargetType: r.TargetType, TargetID: r.TargetID, IPPrefix: r.IpPrefix, Metadata: r.Metadata}
	}
	return out, nil
}
