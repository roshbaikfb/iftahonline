package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestUpdateName (D4).
func TestUpdateName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, _ := f.signup(t)
	me := as(t.Context(), acc)

	u, err := f.svc.UpdateName(me, "  منى أحمد ")
	if err != nil || u.Name != "منى أحمد" || u.ID != acc.User.ID {
		t.Fatalf("rename = %+v, %v", u, err)
	}
	got, _, err := f.svc.Me(me)
	if err != nil || got.Name != "منى أحمد" {
		t.Fatalf("me = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("ا", 101)} {
		if _, err = f.svc.UpdateName(me, bad); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("name %q: %v", bad, err)
		}
	}
	if _, err = f.svc.UpdateName(t.Context(), "x"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("no session: %v", err)
	}
}

// TestChangePassword (D4): the current password is proven; other sessions
// are signed out and this one stays; the user is emailed.
func TestChangePassword(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t)
	me := as(t.Context(), acc)
	other, err := f.svc.Login(f.device(t), email, goodPassword)
	if err != nil {
		t.Fatal(err)
	}

	err = f.svc.ChangePassword(me, "not the password", "a brand new password")
	if e, ok := errors.AsType[*errs.Error](err); !ok || len(e.Fields) != 1 || e.Fields[0].Field != "current_password" {
		t.Fatalf("wrong current: %v", err)
	}
	if err = f.svc.ChangePassword(me, goodPassword, "short"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("weak new: %v", err)
	}
	if err = f.svc.ChangePassword(me, goodPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Authenticate(t.Context(), string(acc.Token)); err != nil {
		t.Fatalf("this session was signed out: %v", err)
	}
	if _, err = f.svc.Authenticate(t.Context(), string(other.Token)); err == nil {
		t.Fatal("the other session still works")
	}
	if _, err = f.svc.Login(f.device(t), email, "a brand new password"); err != nil {
		t.Fatalf("the new password: %v", err)
	}
	var notices int
	if err = f.owner.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE kind = 'identity.password.changed' AND args->>'user_id' = $1`,
		acc.User.ID.String()).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("notices = %d, %v", notices, err)
	}
	if err = f.svc.ChangePassword(t.Context(), "a brand new password", "another new password"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("no session: %v", err)
	}
}

// TestChangePasswordThrottlesGuesses (D4): a stolen session cannot guess
// the current password without limit.
func TestChangePasswordThrottlesGuesses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, _ := f.signup(t)
	me := as(t.Context(), acc)
	var last error
	for range 10 {
		if last = f.svc.ChangePassword(me, "a wrong guess!", "a brand new password"); errs.Is(last, errs.KindRateLimited) {
			break
		}
	}
	if !errs.Is(last, errs.KindRateLimited) {
		t.Fatalf("guesses never throttled: %v", last)
	}
	// Even the right password waits now.
	if err := f.svc.ChangePassword(me, goodPassword, "a brand new password"); !errs.Is(err, errs.KindRateLimited) {
		t.Fatalf("after the budget: %v", err)
	}
}
