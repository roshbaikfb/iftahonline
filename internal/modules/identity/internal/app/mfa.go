package app

import (
	"context"
	"encoding/base32"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TOTPIssuer is the label authenticator apps show.
const TOTPIssuer = "iftahonline"

var (
	errMFAEnabled    = errs.Conflict("mfa_enabled", "two-factor authentication is already enabled")
	errMFANotPending = errs.Conflict("mfa_not_pending", "start two-factor setup first")
	errMFADisabled   = errs.Conflict("mfa_disabled", "two-factor authentication is not enabled")
	errBadSecondCode = errs.Unauthenticated("invalid code")
)

func totpAAD(userID ulid.ID) []byte { return []byte("totp:" + userID.String()) }

// TOTPSetup is shown once, as a QR code, while enrolling.
type TOTPSetup struct {
	Secret string // base32, for manual entry
	URI    string // otpauth://
}

// SetupTOTP starts enrolment: a fresh sealed seed, not active until
// ConfirmTOTP proves the user's app produces codes from it.
func (s *Service) SetupTOTP(ctx context.Context) (TOTPSetup, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return TOTPSetup{}, errNoSession
	}
	u, err := store.UserByID(ctx, s.db, id.UserID)
	if err != nil {
		return TOTPSetup{}, err
	}
	if u.MFAEnabled() {
		return TOTPSetup{}, errMFAEnabled
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return TOTPSetup{}, err
	}
	sealed, err := s.keyring.Seal(secret, totpAAD(u.ID))
	if err != nil {
		return TOTPSetup{}, err
	}
	pending, err := store.SetPendingTOTP(ctx, s.db, u.ID, sealed)
	if err != nil {
		return TOTPSetup{}, err
	}
	if !pending {
		return TOTPSetup{}, errMFAEnabled
	}
	return TOTPSetup{Secret: b32Encode(secret), URI: totp.URI(secret, TOTPIssuer, u.Email)}, nil
}

// ConfirmTOTP enables 2FA once the user enters a valid code, and returns
// the recovery codes (shown once; only hashes are kept).
func (s *Service) ConfirmTOTP(ctx context.Context, code string) ([]string, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return nil, errNoSession
	}
	u, err := store.UserByID(ctx, s.db, id.UserID)
	if err != nil {
		return nil, err
	}
	if u.MFAEnabled() {
		return nil, errMFAEnabled
	}
	if len(u.TOTPSecret) == 0 {
		return nil, errMFANotPending
	}
	secret, err := s.keyring.Open(u.TOTPSecret, totpAAD(u.ID))
	if err != nil {
		return nil, err
	}
	step, ok := totp.Verify(secret, code, s.now(), 0)
	if !ok {
		return nil, errBadSecondCode
	}
	codes, err := domain.NewRecoveryCodes(domain.RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, len(codes))
	for i, c := range codes {
		hashes[i] = domain.HashRecoveryCode(c)
	}
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		enabled, txErr := store.EnableTOTP(ctx, tx, u.ID, step, hashes)
		if txErr != nil {
			return txErr
		}
		if !enabled {
			return errMFAEnabled // a concurrent confirmation won
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns 2FA off. It needs the password and a current second
// factor, so a stolen session alone cannot remove it.
func (s *Service) DisableTOTP(ctx context.Context, password, code string) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	u, err := store.UserByID(ctx, s.db, id.UserID)
	if err != nil {
		return err
	}
	if !u.MFAEnabled() {
		return errMFADisabled
	}
	match, _, err := s.hasher.Verify(ctx, password, u.PasswordHash)
	if err != nil {
		return err
	}
	if !match {
		return errBadSecondCode
	}
	if err := s.checkSecondFactor(ctx, u, code); err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return store.DisableTOTP(ctx, tx, u.ID) })
}

// checkSecondFactor accepts a TOTP code (once per step) or an unused
// recovery code, and throttles failures per user.
func (s *Service) checkSecondFactor(ctx context.Context, u domain.User, code string) error {
	failKey := "mfa:fail:" + u.ID.String()
	if err := s.peek(ctx, failKey, s.limits.MFAFailPerUser); err != nil {
		return err
	}
	ok, err := s.secondFactorOK(ctx, u, code)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.limiter.Allow(ctx, failKey, s.limits.MFAFailPerUser); err != nil {
			return errs.Unavailable(5*time.Second, err)
		}
		return errBadSecondCode
	}
	return nil
}

func (s *Service) secondFactorOK(ctx context.Context, u domain.User, code string) (bool, error) {
	if domain.LooksLikeRecoveryCode(code) {
		return store.UseRecoveryCode(ctx, s.db, u.ID, domain.HashRecoveryCode(code))
	}
	secret, err := s.keyring.Open(u.TOTPSecret, totpAAD(u.ID))
	if err != nil {
		return false, err
	}
	step, ok := totp.Verify(secret, code, s.now(), u.TOTPLastStep)
	if !ok {
		return false, nil
	}
	// Claim the step atomically: two requests with the same code race,
	// and only one wins.
	return store.UseTOTPStep(ctx, s.db, u.ID, step)
}

// startMFA issues the challenge a user with 2FA must complete after the
// first factor.
func (s *Service) startMFA(ctx context.Context, u domain.User) (AuthResult, error) {
	ch, err := s.putChallenge(ctx, s.ids.New().String(), challengeData{Purpose: purposeMFA, UserID: u.ID}, MFATTL)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: u, MFA: &ch}, nil
}

// VerifyMFA completes sign-in with the second factor.
func (s *Service) VerifyMFA(ctx context.Context, challengeID, code string) (AuthResult, error) {
	d, err := s.attempt(ctx, challengeID, purposeMFA)
	if err != nil {
		return AuthResult{}, err
	}
	u, err := store.UserByID(ctx, s.db, d.UserID)
	if errors.Is(err, domain.ErrNotFound) || err == nil && (u.Status != domain.UserActive || !u.MFAEnabled()) {
		s.dropChallenge(ctx, challengeID)
		return AuthResult{}, errBadCode
	}
	if err != nil {
		return AuthResult{}, err
	}
	if err := s.checkSecondFactor(ctx, u, code); err != nil {
		return AuthResult{}, err
	}
	s.dropChallenge(ctx, challengeID)
	return s.openSession(ctx, u)
}

// openSession creates a session for u in its own transaction.
func (s *Service) openSession(ctx context.Context, u domain.User) (AuthResult, error) {
	token, err := authtoken.New()
	if err != nil {
		return AuthResult{}, err
	}
	var sess domain.Session
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		sess, err = s.insertSession(ctx, tx, u.ID, token)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: u, Session: sess, Token: token}, nil
}

func b32Encode(b []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
