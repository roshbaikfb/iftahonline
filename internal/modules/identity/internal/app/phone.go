package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

var (
	errOTPUnavailable = errs.Unavailable(time.Minute, errors.New("identity: phone sign-in is not available"))
	errBadPhone       = errs.Validation(errs.FieldError{Field: "phone", Code: "invalid", Message: "enter a mobile number from a supported country"})
	errPhoneTaken     = errs.Conflict("phone_taken", "this number belongs to another account")
)

// parsePhone accepts +E.164 or a national number in region (default EG).
func parsePhone(raw, region string) (phone.E164, error) {
	r := phone.Region(region)
	if region == "" {
		r = phone.EG
	}
	n, err := phone.Parse(raw, r)
	if err != nil {
		return phone.E164{}, errBadPhone
	}
	return n, nil
}

// otpText is the message carrying a code. It names the purpose so a code
// phished for one thing is less likely to be read out for another.
func otpText(code, purpose string) string {
	what := "تسجيل الدخول"
	if purpose == purposeVerifyPhone {
		what = "تأكيد رقم الهاتف"
	}
	return fmt.Sprintf("رمز %s في iftahonline: %s\nلا تشاركه مع أحد. صالح لمدة 5 دقائق.\n%s code: %s", what, code, purpose, code)
}

// throttleOTP applies the per-number and per-IP send limits. Number keys
// are HMAC-ed so Redis never holds phone numbers in key names.
func (s *Service) throttleOTP(ctx context.Context, n phone.E164) error {
	if err := s.throttle(ctx, "otp:ip:"+ipKey(ctx), s.limits.OTPPerIP); err != nil {
		return err
	}
	if err := s.throttle(ctx, s.limitKey("otp:phone", n.Unmasked()), s.limits.OTPPerPhone); err != nil {
		return err
	}
	return s.throttle(ctx, s.limitKey("otp:phone-day", n.Unmasked()), s.limits.OTPPerPhoneDay)
}

// issueOTP creates a challenge, seals the code for the sender job and
// enqueues it. The job args carry only the challenge ID.
func (s *Service) issueOTP(ctx context.Context, d challengeData, to phone.E164) (Challenge, error) {
	code, err := domain.NewOTP()
	if err != nil {
		return Challenge{}, err
	}
	id := s.ids.New().String()
	d.MAC = domain.OTPMAC(s.pepper, id, d.Purpose, code)
	ch, err := s.putChallenge(ctx, id, d, domain.OTPTTL)
	if err != nil {
		return Challenge{}, err
	}
	if err = s.putDelivery(ctx, deliveryKey(ch.ID), "delivery:"+ch.ID, delivery{Phone: to.Unmasked(), Text: otpText(code, d.Purpose)}, domain.OTPTTL); err != nil {
		return Challenge{}, err
	}
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		_, txErr := s.jobs.InsertTx(ctx, tx, SendOTPArgs{ChallengeID: ch.ID}, nil)
		return txErr
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("identity: enqueue otp: %w", err)
	}
	return ch, nil
}

// StartOTPLogin sends a sign-in code to a verified number. The answer is
// the same whether or not the number belongs to an account (no
// enumeration); unknown numbers simply receive nothing.
func (s *Service) StartOTPLogin(ctx context.Context, rawPhone, region string) (Challenge, error) {
	if !s.otpEnabled {
		return Challenge{}, errOTPUnavailable
	}
	n, err := parsePhone(rawPhone, region)
	if err != nil {
		return Challenge{}, err
	}
	if err = s.throttleOTP(ctx, n); err != nil {
		return Challenge{}, err
	}
	u, err := store.UserByPhone(ctx, s.db, n.Unmasked())
	if errors.Is(err, domain.ErrNotFound) || err == nil && u.Status != domain.UserActive {
		// A decoy that can never be completed.
		return Challenge{ID: s.ids.New().String(), ExpiresAt: s.now().Add(domain.OTPTTL)}, nil
	}
	if err != nil {
		return Challenge{}, err
	}
	return s.issueOTP(ctx, challengeData{Purpose: purposeLogin, UserID: u.ID}, n)
}

// VerifyOTPLogin completes phone sign-in. Users with 2FA get an MFA
// challenge instead of a session.
func (s *Service) VerifyOTPLogin(ctx context.Context, challengeID, code string) (AuthResult, error) {
	d, err := s.attempt(ctx, challengeID, purposeLogin)
	if err != nil {
		return AuthResult{}, err
	}
	if !s.checkCode(challengeID, d, code) {
		return AuthResult{}, errBadCode
	}
	s.dropChallenge(ctx, challengeID)
	u, err := store.UserByID(ctx, s.db, d.UserID)
	if errors.Is(err, domain.ErrNotFound) || err == nil && u.Status != domain.UserActive {
		return AuthResult{}, errBadCode
	}
	if err != nil {
		return AuthResult{}, err
	}
	if u.MFAEnabled() {
		return s.startMFA(ctx, u)
	}
	return s.openSession(ctx, u)
}

// StartPhoneVerification sends a code to a number the signed-in user wants
// to add.
func (s *Service) StartPhoneVerification(ctx context.Context, rawPhone, region string) (Challenge, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return Challenge{}, errNoSession
	}
	if !s.otpEnabled {
		return Challenge{}, errOTPUnavailable
	}
	n, err := parsePhone(rawPhone, region)
	if err != nil {
		return Challenge{}, err
	}
	if err := s.throttleOTP(ctx, n); err != nil {
		return Challenge{}, err
	}
	return s.issueOTP(ctx, challengeData{Purpose: purposeVerifyPhone, UserID: id.UserID, Phone: n.Unmasked()}, n)
}

// VerifyPhone stores the number once the user proves they received the
// code.
func (s *Service) VerifyPhone(ctx context.Context, challengeID, code string) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	d, err := s.attempt(ctx, challengeID, purposeVerifyPhone)
	if err != nil {
		return err
	}
	if d.UserID != id.UserID || !s.checkCode(challengeID, d, code) {
		return errBadCode
	}
	s.dropChallenge(ctx, challengeID)
	err = store.SetVerifiedPhone(ctx, s.db, id.UserID, d.Phone)
	if errors.Is(err, store.ErrPhoneTaken) {
		return errPhoneTaken
	}
	return err
}

// SendOTPArgs delivers a pending code (ADR-0005 §4): the args carry only
// the challenge ID; the number and code wait sealed in Redis.
type SendOTPArgs struct {
	jobs.GlobalArgs
	ChallengeID string `json:"challenge_id"`
}

// Kind implements river.JobArgs.
func (SendOTPArgs) Kind() string { return "identity.otp.send" }
