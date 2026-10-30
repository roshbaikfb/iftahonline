package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Impersonation bounds (ADR-0055).
const (
	MaxImpersonation = 60 * time.Minute
	MinImpersonation = 5 * time.Minute
	// ImpersonationCodeTTL is how long the console's one-time code may
	// wait before the dashboard exchanges it for the session.
	ImpersonationCodeTTL = time.Minute
)

// Impersonation describes an admin's session as a store's owner.
type Impersonation struct {
	Admin     ulid.ID
	Store     tenant.StoreID
	ReadOnly  bool
	ExpiresAt time.Time
}

// ImpersonationGrant is what the console hands the admin: a one-time code
// the dashboard exchanges for the session cookie.
type ImpersonationGrant struct {
	Code          string
	CodeExpiresAt time.Time
	ExpiresAt     time.Time // of the session
}

var (
	errNoOwner         = errs.Conflict("store_has_no_owner", "the store has no owner to act as")
	errBadImpersonate  = errs.Validation(errs.FieldError{Field: "minutes", Code: "out_of_range", Message: "minutes must be between 5 and 60"})
	errImpersonateRead = errs.Forbidden("this support session is read-only")
)

func impersonationKey(code string) string {
	h := sha256.Sum256([]byte(code))
	return "identity:impersonation:" + hex.EncodeToString(h[:])
}

// StartImpersonation opens a session as the owner of the store in ctx for
// admin, for d (5 to 60 minutes, never extended), read-only unless write.
// The store's audit log records it in the same transaction; the admin's
// console has already recorded the attempt with the reason in its own.
func (s *Service) StartImpersonation(ctx context.Context, admin ulid.ID, reason string, d time.Duration, write bool) (ImpersonationGrant, error) {
	if d < MinImpersonation || d > MaxImpersonation {
		return ImpersonationGrant{}, errBadImpersonate
	}
	if admin.IsZero() || reason == "" {
		return ImpersonationGrant{}, errors.New("identity: impersonation needs an admin and a reason")
	}
	token, err := authtoken.New()
	if err != nil {
		return ImpersonationGrant{}, err
	}
	code, err := authtoken.New()
	if err != nil {
		return ImpersonationGrant{}, err
	}
	now := s.now()
	g := ImpersonationGrant{Code: string(code), CodeExpiresAt: now.Add(ImpersonationCodeTTL), ExpiresAt: now.Add(d)}
	sessionID := s.ids.New()
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		owner, txErr := store.Owner(ctx, tx)
		if errors.Is(txErr, domain.ErrNotFound) {
			return errNoOwner
		}
		if txErr != nil {
			return txErr
		}
		if txErr = store.InsertImpersonation(ctx, tx, store.Impersonation{
			ID: sessionID, UserID: owner, Admin: admin, TokenHash: token.Hash(), ExpiresAt: g.ExpiresAt, ReadOnly: !write,
		}); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "impersonation.started", TargetType: "session", TargetID: sessionID.String(),
			Metadata: map[string]any{"impersonated_by": admin.String(), "reason": reason, "read_only": !write, "expires_at": g.ExpiresAt.UTC()}})
	})
	if err != nil {
		return ImpersonationGrant{}, err
	}
	sealed, err := s.keyring.Seal([]byte(token), []byte("impersonation"))
	if err != nil {
		return ImpersonationGrant{}, err
	}
	if err := s.rdb.Set(ctx, impersonationKey(g.Code), sealed, ImpersonationCodeTTL).Err(); err != nil {
		return ImpersonationGrant{}, errs.Unavailable(5*time.Second, fmt.Errorf("identity: store impersonation code: %w", err))
	}
	return g, nil
}

// ExchangeImpersonation trades a one-time code for its session token
// (once: the code is deleted as it is read).
func (s *Service) ExchangeImpersonation(ctx context.Context, code string) (authtoken.Token, time.Time, error) {
	if err := s.throttle(ctx, "impersonation:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return "", time.Time{}, err
	}
	if _, ok := authtoken.Parse(code); !ok {
		return "", time.Time{}, errBadCode
	}
	sealed, err := s.rdb.GetDel(ctx, impersonationKey(code)).Bytes()
	if errors.Is(err, redis.Nil) {
		return "", time.Time{}, errBadCode
	}
	if err != nil {
		return "", time.Time{}, errs.Unavailable(5*time.Second, fmt.Errorf("identity: impersonation code: %w", err))
	}
	raw, err := s.keyring.Open(sealed, []byte("impersonation"))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("identity: open impersonation code: %w", err)
	}
	token := authtoken.Token(raw)
	as, err := store.SessionByToken(ctx, s.db, token)
	if errors.Is(err, domain.ErrNotFound) || err == nil && as.Impersonator == nil {
		return "", time.Time{}, errBadCode
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return token, as.ExpiresAt, nil
}

// RecordImpersonatedRequest writes one store audit entry per request of
// an impersonation session (the actor is the owner, the admin is in the
// metadata), refused or not. Only the method and the route pattern are
// kept: no query strings, no bodies.
func (s *Service) RecordImpersonatedRequest(ctx context.Context, imp Impersonation, method, route string, refused bool) error {
	ctx = tenant.WithStore(ctx, imp.Store)
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "impersonation.request", TargetType: "route", TargetID: route,
			Metadata: map[string]any{"impersonated_by": imp.Admin.String(), "method": method, "refused": refused}})
	})
}

// ErrImpersonationReadOnly is the answer to a change in a read-only
// impersonation session.
func ErrImpersonationReadOnly() error { return errImpersonateRead }

func impersonationOf(as store.ActiveSession) *Impersonation {
	if as.Impersonator == nil || as.ScopeStore == nil {
		return nil
	}
	return &Impersonation{Admin: *as.Impersonator, Store: tenant.StoreID{ID: *as.ScopeStore}, ReadOnly: as.ReadOnly, ExpiresAt: as.ExpiresAt}
}

// impersonating returns the admin acting through the principal in ctx.
func impersonating(ctx context.Context) (ulid.ID, bool) {
	p, ok := authz.PrincipalFrom(ctx)
	return p.ImpersonatedBy, ok && !p.ImpersonatedBy.IsZero()
}
