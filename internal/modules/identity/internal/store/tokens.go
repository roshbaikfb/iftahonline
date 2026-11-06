package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store/identitydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// One-time email links (D3): resetting a password, confirming an email.

// Link purposes.
const (
	PurposePasswordReset = "password_reset"
	PurposeEmailVerify   = "email_verify"
)

// NewUserToken is a link to record: only its hash.
type NewUserToken struct {
	ID        ulid.ID
	UserID    ulid.ID
	Purpose   string
	TokenHash []byte
	Email     string
	ExpiresAt time.Time
}

// InsertUserToken records a link, closing the user's older ones of its
// purpose: only the latest works.
func InsertUserToken(ctx context.Context, tx Tx, t NewUserToken) error {
	q := identitydb.New(tx)
	if err := q.CloseUserTokens(ctx, identitydb.CloseUserTokensParams{UserID: t.UserID, Purpose: t.Purpose}); err != nil {
		return fmt.Errorf("identity store: close links: %w", err)
	}
	if err := q.InsertUserToken(ctx, identitydb.InsertUserTokenParams{ID: t.ID, UserID: t.UserID, Purpose: t.Purpose, TokenHash: t.TokenHash,
		Email: t.Email, ExpiresAt: t.ExpiresAt}); err != nil {
		return fmt.Errorf("identity store: insert link: %w", err)
	}
	return nil
}

// UsedToken is a link taken: whose, and the address it was sent to.
type UsedToken struct {
	ID     ulid.ID
	UserID ulid.ID
	Email  string
}

// UseUserToken takes an open link of purpose once; domain.ErrNotFound when
// it is unknown, used or expired.
func UseUserToken(ctx context.Context, tx Tx, hash []byte, purpose string) (UsedToken, error) {
	r, err := identitydb.New(tx).UseUserToken(ctx, identitydb.UseUserTokenParams{TokenHash: hash, Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return UsedToken{}, domain.ErrNotFound
	}
	if err != nil {
		return UsedToken{}, fmt.Errorf("identity store: use link: %w", err)
	}
	return UsedToken{ID: r.ID, UserID: r.UserID, Email: r.Email}, nil
}

// OpenTokenEmail is the email an open link of purpose is for, without
// using it; domain.ErrNotFound when it is unknown, used or expired.
func OpenTokenEmail(ctx context.Context, db Tx, hash []byte, purpose string) (string, error) {
	email, err := identitydb.New(db).GetOpenUserTokenEmail(ctx, identitydb.GetOpenUserTokenEmailParams{TokenHash: hash, Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("identity store: check link: %w", err)
	}
	return email, nil
}

// TokenToSend is an open link its email job sends.
type TokenToSend struct {
	ID        ulid.ID
	UserID    ulid.ID
	Purpose   string
	Email     string
	Name      string
	ExpiresAt time.Time
}

// TokenForSend reads an open link; domain.ErrNotFound when it was used,
// expired or replaced meanwhile.
func TokenForSend(ctx context.Context, db Tx, id ulid.ID) (TokenToSend, error) {
	r, err := identitydb.New(db).GetUserTokenForSend(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenToSend{}, domain.ErrNotFound
	}
	if err != nil {
		return TokenToSend{}, fmt.Errorf("identity store: link to send: %w", err)
	}
	return TokenToSend{ID: r.ID, UserID: r.UserID, Purpose: r.Purpose, Email: r.Email, Name: r.Name, ExpiresAt: r.ExpiresAt}, nil
}

// SetEmailVerified confirms email for the user, if it is still theirs and
// not confirmed yet; ok is false otherwise.
func SetEmailVerified(ctx context.Context, tx Tx, userID ulid.ID, email string) (bool, error) {
	n, err := identitydb.New(tx).SetEmailVerified(ctx, identitydb.SetEmailVerifiedParams{ID: userID, Email: email})
	if err != nil {
		return false, fmt.Errorf("identity store: verify email: %w", err)
	}
	return n == 1, nil
}

// RevokeUserSessions signs a user out everywhere but keep (zero: all).
func RevokeUserSessions(ctx context.Context, tx Tx, userID, keep ulid.ID) error {
	p := identitydb.RevokeUserSessionsParams{UserID: userID}
	if !keep.IsZero() {
		p.Keep = &keep
	}
	if err := identitydb.New(tx).RevokeUserSessions(ctx, p); err != nil {
		return fmt.Errorf("identity store: revoke sessions: %w", err)
	}
	return nil
}
