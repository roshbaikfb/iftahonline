package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// sendLink runs the email job of the user's latest link of purpose and
// returns the link's secret, after the page's path.
func (f fixture) sendLink(t *testing.T, email, purpose, path string) (string, string) {
	t.Helper()
	var id ulid.ID
	if err := f.owner.QueryRow(t.Context(), `SELECT t.id FROM user_tokens t JOIN users u ON u.id = t.user_id
		WHERE u.email = $1 AND t.purpose = $2 ORDER BY t.created_at DESC LIMIT 1`, email, purpose).Scan(&id); err != nil {
		t.Fatalf("no %s link for %s: %v", purpose, email, err)
	}
	c := &capture{}
	w := &app.LinkEmailWorker{D: f.workerDeps(c)}
	if err := w.Work(t.Context(), &river.Job[app.LinkEmailArgs]{Args: app.LinkEmailArgs{TokenID: id}}); err != nil {
		t.Fatal(err)
	}
	if len(c.mail) != 1 || c.mail[0].To != email {
		t.Fatalf("link mails = %+v", c.mail)
	}
	_, after, ok := strings.Cut(c.mail[0].Text, path+"#")
	if !ok {
		t.Fatalf("no %s link in %q", path, c.mail[0].Text)
	}
	return strings.Fields(after)[0], c.mail[0].Subject
}

// TestPasswordReset (D3): a reset link is emailed only to an account, used
// once, signs every session out, tells the user, and signs in with the new
// password; an unknown email gets the same answer.
func TestPasswordReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t)

	if err := f.svc.ForgotPassword(f.device(t), "nobody-"+email); err != nil {
		t.Fatalf("an unknown email: %v", err)
	}
	if err := f.svc.ForgotPassword(f.device(t), "not an email"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a malformed email: %v", err)
	}
	if err := f.svc.ForgotPassword(f.device(t), strings.ToUpper(email)); err != nil {
		t.Fatal(err)
	}
	tok, subject := f.sendLink(t, email, "password_reset", "/reset-password")
	if !strings.Contains(subject, "كلمة مرور جديدة") {
		t.Fatalf("subject = %q", subject)
	}

	// Checking the link shows its email and leaves it open.
	for range 2 {
		if got, err := f.svc.CheckResetLink(f.device(t), tok); err != nil || got != email {
			t.Fatalf("check = %q, %v", got, err)
		}
	}
	if _, err := f.svc.CheckResetLink(f.device(t), "junk"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("check junk: %v", err)
	}
	// A refused password rolls back: the link stays open for another try.
	if _, err := f.svc.ResetPassword(f.device(t), tok, "short"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a weak password: %v", err)
	}
	res, err := f.svc.ResetPassword(f.device(t), tok, "a brand new password")
	if err != nil || res.MFA != nil || res.Token == "" || res.User.Email != email {
		t.Fatalf("reset = %+v, %v", res, err)
	}
	// Used once.
	if _, err = f.svc.CheckResetLink(f.device(t), tok); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("check a used link: %v", err)
	}
	if _, err = f.svc.ResetPassword(f.device(t), tok, "another new password"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("a used link: %v", err)
	}
	if _, err = f.svc.ResetPassword(f.device(t), "junk", "another new password"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("a malformed link: %v", err)
	}
	// Every older session is signed out; the new password signs in.
	if _, err = f.svc.Authenticate(t.Context(), string(acc.Token)); err == nil {
		t.Fatal("the old session still works")
	}
	if _, err = f.svc.Authenticate(t.Context(), string(res.Token)); err != nil {
		t.Fatalf("the reset's session: %v", err)
	}
	if _, err = f.svc.Login(f.device(t), email, goodPassword); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err = f.svc.Login(f.device(t), email, "a brand new password"); err != nil {
		t.Fatalf("the new password: %v", err)
	}
	// The user is told (the notice job).
	var notices int
	if err = f.owner.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE kind = 'identity.password.changed' AND args->>'user_id' = $1`,
		acc.User.ID.String()).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("password notices = %d, %v", notices, err)
	}
	c := &capture{}
	if err = (&app.PasswordChangedWorker{D: f.workerDeps(c)}).Work(t.Context(), &river.Job[app.PasswordChangedArgs]{
		Args: app.PasswordChangedArgs{UserID: acc.User.ID, At: time.Now()}}); err != nil || len(c.mail) != 1 || !strings.Contains(c.mail[0].Text, "/forgot-password") {
		t.Fatalf("notice = %+v, %v", c.mail, err)
	}
}

// TestPasswordResetAsksTheSecondFactor (D3): a mailbox alone never bypasses
// two-step verification.
func TestPasswordResetAsksTheSecondFactor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t)
	me := as(t.Context(), acc)
	setup, err := f.svc.SetupTOTP(me)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ConfirmTOTP(me, totpNow(t, setup.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.ForgotPassword(f.device(t), email); err != nil {
		t.Fatal(err)
	}
	tok, _ := f.sendLink(t, email, "password_reset", "/reset-password")
	res, err := f.svc.ResetPassword(f.device(t), tok, "a brand new password")
	if err != nil || res.MFA == nil || res.Token != "" {
		t.Fatalf("reset with 2FA = %+v, %v", res, err)
	}
}

// TestEmailVerification (D3): sign-up sends a confirmation link; it
// confirms the email once; a confirmed user is sent nothing more; an
// invited member's email is confirmed by the invitation itself.
func TestEmailVerification(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t)
	if acc.User.EmailVerifiedAt != nil {
		t.Fatal("confirmed at sign-up")
	}
	tok, subject := f.sendLink(t, email, "email_verify", "/verify-email")
	if !strings.Contains(subject, "أكّد بريدك") {
		t.Fatalf("subject = %q", subject)
	}
	// Asking again replaces the link: the first no longer works.
	if err := f.svc.SendEmailVerification(as(t.Context(), acc)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VerifyEmail(f.device(t), tok); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("a replaced link: %v", err)
	}
	tok, _ = f.sendLink(t, email, "email_verify", "/verify-email")
	if got, err := f.svc.VerifyEmail(f.device(t), tok); err != nil || got != email {
		t.Fatalf("verify = %q, %v", got, err)
	}
	if _, err := f.svc.VerifyEmail(f.device(t), tok); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("a used link: %v", err)
	}
	u, _, err := f.svc.Me(as(t.Context(), acc))
	if err != nil || u.EmailVerifiedAt == nil {
		t.Fatalf("me = %+v, %v", u, err)
	}
	// Confirmed: nothing more is sent.
	var before, after int
	_ = f.owner.QueryRow(t.Context(), `SELECT count(*) FROM user_tokens WHERE user_id = $1`, acc.User.ID).Scan(&before)
	if err = f.svc.SendEmailVerification(as(t.Context(), acc)); err != nil {
		t.Fatal(err)
	}
	_ = f.owner.QueryRow(t.Context(), `SELECT count(*) FROM user_tokens WHERE user_id = $1`, acc.User.ID).Scan(&after)
	if after != before {
		t.Fatalf("a confirmed email got another link (%d → %d)", before, after)
	}
	if err = f.svc.SendEmailVerification(t.Context()); err == nil {
		t.Fatal("sent without a session")
	}

	// An invited member's new account is confirmed by the invitation.
	inv, err := f.svc.Invite(ownerCtx(f.device(t), acc), f.unique("joiner")+"@example.com", "support")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := f.svc.AcceptInvitation(f.device(t), app.AcceptInput{Token: f.inviteToken(t, acc.StoreID, inv.ID), Name: "Sara", Password: goodPassword})
	if err != nil || joined.User.EmailVerifiedAt == nil {
		t.Fatalf("joined = %+v, %v", joined.User, err)
	}
}
