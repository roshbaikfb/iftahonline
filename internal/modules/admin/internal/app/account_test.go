package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// anotherDevice opens one more session for admin id straight in the
// database (a third sign-in would need a third time step).
func (f fixture) anotherDevice(t *testing.T, id ulid.ID) authtoken.Token {
	t.Helper()
	tok, err := authtoken.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertSession(t.Context(), f.deps.DB, f.ids.New(), id, tok, "Safari/iPhone", "10.0.0.0/24", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return tok
}

func fieldIs(err error, field, code string) bool {
	e, ok := errors.AsType[*errs.Error](err)
	if !ok {
		return false
	}
	for _, f := range e.Fields {
		if f.Field == field && f.Code == code {
			return true
		}
	}
	return false
}

// TestOwnAccount (C3): an admin renames themselves, changes the password
// with the current one (other devices signed out, an email queued), and
// sees and ends their other sessions.
func TestOwnAccount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c, email := f.createAdmin(t)
	ctx := f.signedInWith(t, c, email)
	other, err := f.svc.Login(f.device(t), email, adminPW, code(t, c.Secret, time.Now().Add(30*time.Second)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = f.svc.UpdateName(ctx, " "); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an empty name: %v", err)
	}
	if a, renameErr := f.svc.UpdateName(ctx, " سارة علي "); renameErr != nil || a.Name != "سارة علي" {
		t.Fatalf("rename = %+v, %v", a, renameErr)
	}

	ss, err := f.svc.Sessions(ctx)
	if err != nil || len(ss) != 2 {
		t.Fatalf("sessions = %+v, %v", ss, err)
	}
	var current, another app.OwnSession
	for _, s := range ss {
		if s.Current {
			current = s
		} else {
			another = s
		}
	}
	if current.ID.IsZero() || another.ID.IsZero() {
		t.Fatalf("sessions = %+v", ss)
	}
	if err = f.svc.RevokeSession(ctx, current.ID); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("revoke this device: %v", err)
	}
	if err = f.svc.RevokeSession(ctx, f.ids.New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("revoke an unknown session: %v", err)
	}

	if err = f.svc.ChangePassword(ctx, "not the password", "a brand new admin passphrase"); !fieldIs(err, "current_password", "wrong") {
		t.Fatalf("a wrong current password: %v", err)
	}
	if err = f.svc.ChangePassword(ctx, adminPW, "short"); !fieldIs(err, "new_password", "weak") {
		t.Fatalf("a weak password: %v", err)
	}
	if err = f.svc.ChangePassword(ctx, adminPW, "a brand new admin passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Authenticate(t.Context(), string(other.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("the other device after a password change: %v", err)
	}
	if ss, _ = f.svc.Sessions(ctx); len(ss) != 1 || !ss[0].Current {
		t.Fatalf("sessions after = %+v", ss)
	}

	// The notice is mailed once.
	box := &mailBox{}
	d := f.deps
	d.Mailer, d.ConsoleURL = box, "https://admin.iftahonline.com/console"
	worker := app.NewService(d)
	for range 2 {
		if err = worker.DeliverNotices(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Other tests' admins share the outbox: count this admin's mail only.
	var mine []string
	for _, m := range box.sent {
		if m.To == email {
			mine = append(mine, m.Subject)
		}
	}
	if len(mine) != 1 || mine[0] != "تغيّرت كلمة مرور حسابك في الكونسول" {
		t.Fatalf("notices = %v", mine)
	}

	// Signing out of the other devices.
	third := f.anotherDevice(t, c.Admin.ID)
	if err = f.svc.RevokeOtherSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Authenticate(t.Context(), string(third)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("a device after signing out of the others: %v", err)
	}
	if _, err = f.svc.Sessions(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}

// TestNewPhone (C3): a code from the old phone starts the move, a code
// from the new one finishes it; then only the new phone signs in.
func TestNewPhone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c, email := f.createAdmin(t)
	ctx := f.signedInWith(t, c, email)

	if err := f.svc.ConfirmNewPhone(ctx, "123456"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("confirm before starting: %v", err)
	}
	if _, err := f.svc.StartNewPhone(ctx, "000000"); !fieldIs(err, "code", "invalid") {
		t.Fatalf("a wrong old code: %v", err)
	}
	p, err := f.svc.StartNewPhone(ctx, code(t, c.Secret, time.Now().Add(30*time.Second)))
	if err != nil || p.Secret == "" || p.Secret == c.Secret || p.QR == "" {
		t.Fatalf("start = %+v, %v", p, err)
	}
	if err = f.svc.ConfirmNewPhone(ctx, code(t, c.Secret, time.Now())); !fieldIs(err, "code", "invalid") {
		t.Fatalf("the old phone's code to confirm: %v", err)
	}
	if err = f.svc.ConfirmNewPhone(ctx, code(t, p.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.ConfirmNewPhone(ctx, code(t, p.Secret, time.Now())); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("confirm twice: %v", err)
	}
	if _, err = f.svc.Login(f.device(t), email, adminPW, code(t, c.Secret, time.Now().Add(60*time.Second))); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("the old phone signs in: %v", err)
	}
	if _, err = f.svc.Login(f.device(t), email, adminPW, code(t, p.Secret, time.Now().Add(30*time.Second))); err != nil {
		t.Fatalf("the new phone signs in: %v", err)
	}
}
