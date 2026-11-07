package httpapi_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
)

// putLink records an open link of purpose for acc straight in the database
// (its email goes through a job these tests do not run) and returns it.
func (f fixture) putLink(t *testing.T, acc account, purpose string) string {
	t.Helper()
	tok, err := authtoken.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(t.Context(), `INSERT INTO user_tokens (id, user_id, purpose, token_hash, email, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, f.ids.New(), mustULID(t, acc.userID), purpose, tok.Hash(), acc.email, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return string(tok)
}

// TestPasswordResetOverHTTP (D3): forgot answers the same for any email; a
// reset signs in with a cookie and signs every other session out.
func TestPasswordResetOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")

	for _, email := range []string{acc.email, "nobody-" + f.unique("x") + "@example.com"} {
		if res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/password/forgot", fmt.Sprintf(`{"email":%q}`, email))); res.StatusCode != http.StatusAccepted {
			t.Fatalf("forgot %s = %d %v", email, res.StatusCode, m)
		}
	}

	tok := f.putLink(t, acc, "password_reset")
	if res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/password/reset/check", fmt.Sprintf(`{"token":%q}`, tok))); res.StatusCode != http.StatusOK || m["email"] != acc.email {
		t.Fatalf("check = %d %v", res.StatusCode, m)
	}
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":"short"}`, tok)))
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("weak = %d %v", res.StatusCode, m)
	}
	res, m = f.do(t, newReq(t, http.MethodPost, "/v1/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":"a brand new password"}`, tok)))
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Set-Cookie"), "__Host-sid=") || m["user"].(map[string]any)["email"] != acc.email {
		t.Fatalf("reset = %d %v", res.StatusCode, m)
	}
	if res, _ = f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(acc.token))); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the old session after a reset = %d", res.StatusCode)
	}
	res, m = f.do(t, newReq(t, http.MethodPost, "/v1/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":"another new password"}`, tok)))
	if res.StatusCode != http.StatusNotFound || m["code"] != "link_not_found" {
		t.Fatalf("reuse = %d %v", res.StatusCode, m)
	}
	login := fmt.Sprintf(`{"email":%q,"password":"a brand new password"}`, acc.email)
	if res, m = f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", login)); res.StatusCode != http.StatusOK {
		t.Fatalf("login with the new password = %d %v", res.StatusCode, m)
	}
}

// TestEmailVerificationOverHTTP (D3).
func TestEmailVerificationOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "mobile")

	if res, _ := f.do(t, newReq(t, http.MethodPost, "/v1/me/email/verification", "")); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("resend signed out = %d", res.StatusCode)
	}
	if res, m := f.do(t, newReq(t, http.MethodPost, "/v1/me/email/verification", "", bearer(acc.token))); res.StatusCode != http.StatusAccepted {
		t.Fatalf("resend = %d %v", res.StatusCode, m)
	}
	tok := f.putLink(t, acc, "email_verify")
	if res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/email/verify", fmt.Sprintf(`{"token":%q}`, tok))); res.StatusCode != http.StatusOK || m["email"] != acc.email {
		t.Fatalf("verify = %d %v", res.StatusCode, m)
	}
	_, m := f.do(t, newReq(t, http.MethodGet, "/v1/me", "", bearer(acc.token)))
	if m["user"].(map[string]any)["email_verified"] != true {
		t.Fatalf("me = %v", m)
	}
	// A reset link does not confirm an email, nor the reverse.
	reset := f.putLink(t, acc, "password_reset")
	if res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/email/verify", fmt.Sprintf(`{"token":%q}`, reset))); res.StatusCode != http.StatusNotFound {
		t.Fatalf("verify with a reset link = %d %v", res.StatusCode, m)
	}
}

func TestLinkValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tests := []struct {
		name, path, body string
		status           int
		code             string
	}{
		{"forgot bad email", "/v1/auth/password/forgot", `{"email":"nope"}`, http.StatusUnprocessableEntity, ""},
		{"forgot malformed", "/v1/auth/password/forgot", `{"email":`, http.StatusBadRequest, ""},
		{"reset junk link", "/v1/auth/password/reset", `{"token":"junk","password":"a brand new password"}`, http.StatusNotFound, "link_not_found"},
		{"reset malformed", "/v1/auth/password/reset", `{"token":`, http.StatusBadRequest, ""},
		{"check junk link", "/v1/auth/password/reset/check", `{"token":"junk"}`, http.StatusNotFound, "link_not_found"},
		{"check malformed", "/v1/auth/password/reset/check", `{"token":`, http.StatusBadRequest, ""},
		{"verify junk link", "/v1/auth/email/verify", `{"token":"junk"}`, http.StatusNotFound, "link_not_found"},
		{"verify malformed", "/v1/auth/email/verify", `{"token":`, http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, m := f.do(t, newReq(t, http.MethodPost, tt.path, tt.body))
			if res.StatusCode != tt.status || (tt.code != "" && m["code"] != tt.code) {
				t.Fatalf("= %d %v; want %d %q", res.StatusCode, m, tt.status, tt.code)
			}
		})
	}
}

// putSupportRequest records an open MFA-reset request on acc in its
// store, with a cancel link for acc, straight in the database (its email
// goes through a job these tests do not run), and returns the link.
func (f fixture) putSupportRequest(t *testing.T, acc account) string {
	t.Helper()
	tok, err := authtoken.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tx, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	store, user, req := mustULID(t, acc.storeID), mustULID(t, acc.userID), f.ids.New()
	if _, err = tx.Exec(ctx, `SELECT set_config('app.store_id', ($1::uuid)::text, true)`, store); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO support_requests (id, store_id, kind, user_id, requested_by, reason, effective_at)
		VALUES ($1, $2, 'mfa_reset', $3, $4, 'ticket', $5)`, req, store, user, f.ids.New(), time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_tokens (id, user_id, purpose, token_hash, email, expires_at, request_id, link_store_id)
		VALUES ($1, $2, 'support_cancel', $3, $4, $5, $6, $7)`, f.ids.New(), user, tok.Hash(), acc.email, time.Now().Add(24*time.Hour), req, store); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return string(tok)
}

// TestSupportLinkOverHTTP (C1): a support request's emailed link is
// checked and cancelled without a session; once cancelled it is gone; a
// malformed body is refused.
func TestSupportLinkOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")
	tok := f.putSupportRequest(t, acc)
	body := fmt.Sprintf(`{"token":%q}`, tok)

	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/support-requests/check", body))
	if res.StatusCode != http.StatusOK || m["kind"] != "mfa_reset" || m["email"] == acc.email || m["effective_at"] == nil {
		t.Fatalf("check = %d %v", res.StatusCode, m)
	}
	if res, m = f.do(t, newReq(t, http.MethodPost, "/v1/support-requests/check", `{"token":"junk"}`)); res.StatusCode != http.StatusNotFound || m["code"] != "link_not_found" {
		t.Fatalf("check junk = %d %v", res.StatusCode, m)
	}
	if res, _ = f.do(t, newReq(t, http.MethodPost, "/v1/support-requests/cancel", `{"token":`)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed = %d", res.StatusCode)
	}
	if res, m = f.do(t, newReq(t, http.MethodPost, "/v1/support-requests/cancel", body)); res.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d %v", res.StatusCode, m)
	}
	for _, path := range []string{"/v1/support-requests/cancel", "/v1/support-requests/check"} {
		if res, m = f.do(t, newReq(t, http.MethodPost, path, body)); res.StatusCode != http.StatusNotFound || m["code"] != "link_not_found" {
			t.Fatalf("%s after cancel = %d %v", path, res.StatusCode, m)
		}
	}
}
