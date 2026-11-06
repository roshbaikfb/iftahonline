package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	mailaddr "github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// One-time email links (D3, the approved mockups): a forgotten password is
// reset from a link that works an hour; an email address is confirmed from
// one that works two days. A link is "<secret>" (authtoken: 256 bits),
// sent in the URL fragment; only its SHA-256 is stored, and the link waits
// sealed in Redis until its email job sends it, as invitations do. A newer
// link of a purpose closes the older ones.
const (
	ResetLinkTTL  = time.Hour
	VerifyLinkTTL = 48 * time.Hour
)

var errLinkGone = errs.NotFound("link_not_found", "this link was used, replaced or has expired")

func linkDeliveryKey(id ulid.ID) string { return "identity:link-delivery:" + id.String() }
func linkAAD(id ulid.ID) string         { return "link:" + id.String() }

// LinkEmailArgs sends a link's email.
type LinkEmailArgs struct {
	jobs.GlobalArgs
	TokenID ulid.ID `json:"token_id"`
}

// Kind implements river.JobArgs.
func (LinkEmailArgs) Kind() string { return "identity.link.email" }

// PasswordChangedArgs tells a user their password changed (reset or
// changed from the account page), so a change they did not make is seen.
type PasswordChangedArgs struct {
	jobs.GlobalArgs
	UserID ulid.ID   `json:"user_id"`
	At     time.Time `json:"at"`
}

// Kind implements river.JobArgs.
func (PasswordChangedArgs) Kind() string { return "identity.password.changed" }

// issueLinkTx records a link of purpose for u and enqueues its email, in
// tx; the link's secret waits sealed for the job.
func (s *Service) issueLinkTx(ctx context.Context, tx pgx.Tx, u domain.User, purpose string, ttl time.Duration) error {
	tok, err := authtoken.New()
	if err != nil {
		return err
	}
	id := s.ids.New()
	if err = store.InsertUserToken(ctx, tx, store.NewUserToken{ID: id, UserID: u.ID, Purpose: purpose, TokenHash: tok.Hash(), Email: u.Email,
		ExpiresAt: s.now().Add(ttl)}); err != nil {
		return err
	}
	if err = s.putDelivery(ctx, linkDeliveryKey(id), linkAAD(id), delivery{Text: string(tok)}, ttl); err != nil {
		return err
	}
	if _, err = s.jobs.InsertTx(ctx, tx, LinkEmailArgs{TokenID: id}, nil); err != nil {
		return fmt.Errorf("identity: enqueue link email: %w", err)
	}
	return nil
}

// ForgotPassword sends a reset link to rawEmail when it has an active
// account. It answers the same either way, so it never tells whether an
// email has an account; requests are throttled per IP and per email.
func (s *Service) ForgotPassword(ctx context.Context, rawEmail string) error {
	if err := s.throttle(ctx, "forgot:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return err
	}
	email, ok := mailaddr.Normalize(rawEmail)
	if !ok {
		return errs.Validation(errs.FieldError{Field: "email", Code: "invalid", Message: "enter a valid email address"})
	}
	if err := s.throttle(ctx, s.limitKey("forgot:email:", email), s.limits.LinksPerEmail); err != nil {
		return err
	}
	u, err := store.UserByEmail(ctx, s.db, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.Status != domain.UserActive {
		return nil
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		return s.issueLinkTx(ctx, tx, u, store.PurposePasswordReset, ResetLinkTTL)
	})
}

// ResetPassword sets a new password from a reset link: the link is used
// once; every session is signed out and the user is told by email. The
// reset signs in like a login: a second factor first when the account has
// one, so a mailbox alone never bypasses it.
func (s *Service) ResetPassword(ctx context.Context, rawToken, password string) (AuthResult, error) {
	if err := s.throttle(ctx, "reset:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return AuthResult{}, err
	}
	tok, ok := authtoken.Parse(rawToken)
	if !ok {
		return AuthResult{}, errLinkGone
	}
	var u domain.User
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		used, txErr := store.UseUserToken(ctx, tx, tok.Hash(), store.PurposePasswordReset)
		if errors.Is(txErr, domain.ErrNotFound) {
			return errLinkGone
		}
		if txErr != nil {
			return txErr
		}
		if u, txErr = store.UserByID(ctx, tx, used.UserID); txErr != nil {
			return txErr
		}
		if u.Status != domain.UserActive {
			return errLinkGone
		}
		if fe := domain.ValidatePassword(password, u.Email); fe != nil {
			return errs.Validation(*fe)
		}
		hash, txErr := s.hasher.Hash(ctx, password)
		if txErr != nil {
			return txErr
		}
		if txErr = store.SetPasswordHash(ctx, tx, u.ID, hash); txErr != nil {
			return txErr
		}
		if txErr = store.RevokeUserSessions(ctx, tx, u.ID, ulid.ID{}); txErr != nil {
			return txErr
		}
		if _, txErr = s.jobs.InsertTx(ctx, tx, PasswordChangedArgs{UserID: u.ID, At: s.now().UTC()}, nil); txErr != nil {
			return fmt.Errorf("identity: enqueue password notice: %w", txErr)
		}
		return nil
	})
	if err != nil {
		return AuthResult{}, err
	}
	if u.MFAEnabled() {
		return s.startMFA(ctx, u)
	}
	return s.openSession(ctx, u)
}

// CheckResetLink answers the email an open reset link is for, without
// using it: the page shows it before asking for the new password.
func (s *Service) CheckResetLink(ctx context.Context, rawToken string) (string, error) {
	if err := s.throttle(ctx, "reset:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return "", err
	}
	tok, ok := authtoken.Parse(rawToken)
	if !ok {
		return "", errLinkGone
	}
	email, err := store.OpenTokenEmail(ctx, s.db, tok.Hash(), store.PurposePasswordReset)
	if errors.Is(err, domain.ErrNotFound) {
		return "", errLinkGone
	}
	return email, err
}

// SendEmailVerification sends the signed-in user a link confirming their
// email: nothing when it is confirmed already. At most one a minute.
func (s *Service) SendEmailVerification(ctx context.Context) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if err := s.throttle(ctx, "verify:user:"+id.UserID.String(), s.limits.LinksPerEmail); err != nil {
		return err
	}
	u, err := store.UserByID(ctx, s.db, id.UserID)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return nil
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		return s.issueLinkTx(ctx, tx, u, store.PurposeEmailVerify, VerifyLinkTTL)
	})
}

// VerifyEmail confirms an email from its link (no session needed: the
// link may open in another browser) and answers the email. A link for an
// address the user no longer has confirms nothing.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) (string, error) {
	if err := s.throttle(ctx, "verify:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return "", err
	}
	tok, ok := authtoken.Parse(rawToken)
	if !ok {
		return "", errLinkGone
	}
	var email string
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		used, err := store.UseUserToken(ctx, tx, tok.Hash(), store.PurposeEmailVerify)
		if errors.Is(err, domain.ErrNotFound) {
			return errLinkGone
		}
		if err != nil {
			return err
		}
		email = used.Email
		_, err = store.SetEmailVerified(ctx, tx, used.UserID, used.Email)
		return err
	})
	return email, err
}

// sendVerificationAtSignup is the first confirmation email, enqueued in the
// sign-up's transaction.
func (s *Service) sendVerificationAtSignup(ctx context.Context, tx pgx.Tx, u domain.User) error {
	return s.issueLinkTx(ctx, tx, u, store.PurposeEmailVerify, VerifyLinkTTL)
}
