package httpapi_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestAccountOverHTTP (D4): rename, then change the password; the session
// that changed it stays, the others are signed out.
func TestAccountOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")
	other := f.login(t, acc.email, "correct horse battery")

	res, m := f.do(t, newReq(t, http.MethodPatch, "/v1/me", `{"name":"  منى أحمد "}`, cookie(acc.token), sameOrigin))
	if res.StatusCode != http.StatusOK || m["name"] != "منى أحمد" || m["email"] != acc.email {
		t.Fatalf("rename = %d %v", res.StatusCode, m)
	}

	change := func(current, next string) (result, map[string]any) {
		return f.do(t, newReq(t, http.MethodPost, "/v1/me/password", fmt.Sprintf(`{"current_password":%q,"new_password":%q}`, current, next), cookie(acc.token), sameOrigin))
	}
	res, m = change("not the password", "a brand new password")
	if errs, _ := m["errors"].([]any); res.StatusCode != http.StatusUnprocessableEntity || len(errs) != 1 || errs[0].(map[string]any)["field"] != "current_password" {
		t.Fatalf("wrong current = %d %v", res.StatusCode, m)
	}
	if res, m = change("correct horse battery", "a brand new password"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("change = %d %v", res.StatusCode, m)
	}
	if res, _ = f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(acc.token))); res.StatusCode != http.StatusOK {
		t.Fatalf("this session after the change = %d", res.StatusCode)
	}
	if res, _ = f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(other))); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the other session after the change = %d", res.StatusCode)
	}
}

func TestAccountValidationAndAuth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "mobile")
	tests := []struct {
		name   string
		r      *http.Request
		status int
	}{
		{"rename signed out", newReq(t, http.MethodPatch, "/v1/me", `{"name":"x"}`), http.StatusUnauthorized},
		{"password signed out", newReq(t, http.MethodPost, "/v1/me/password", `{"current_password":"x","new_password":"a brand new password"}`), http.StatusUnauthorized},
		{"empty name", newReq(t, http.MethodPatch, "/v1/me", `{"name":"   "}`, bearer(acc.token)), http.StatusUnprocessableEntity},
		{"long name", newReq(t, http.MethodPatch, "/v1/me", fmt.Sprintf(`{"name":%q}`, strings.Repeat("a", 101)), bearer(acc.token)), http.StatusUnprocessableEntity},
		{"rename malformed", newReq(t, http.MethodPatch, "/v1/me", `{"name":`, bearer(acc.token)), http.StatusBadRequest},
		{"weak new password", newReq(t, http.MethodPost, "/v1/me/password", `{"current_password":"correct horse battery","new_password":"short"}`, bearer(acc.token)), http.StatusUnprocessableEntity},
		{"password malformed", newReq(t, http.MethodPost, "/v1/me/password", `{"current_password":`, bearer(acc.token)), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if res, m := f.do(t, tt.r); res.StatusCode != tt.status {
				t.Fatalf("= %d %v; want %d", res.StatusCode, m, tt.status)
			}
		})
	}
}

// TestTOTPSetupHasQR (D4): the account page shows the seed as a QR code.
func TestTOTPSetupHasQR(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "mobile")
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/me/mfa/totp", "", bearer(acc.token)))
	if svg, _ := m["qr_svg"].(string); res.StatusCode != http.StatusOK || !strings.HasPrefix(svg, "<svg ") || m["secret"] == "" {
		t.Fatalf("setup = %d %v", res.StatusCode, m)
	}
}

// login opens another web session for email and returns its cookie.
func (f fixture) login(t *testing.T, email, password string) string {
	t.Helper()
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login = %d %v", res.StatusCode, m)
	}
	for _, c := range res.Cookies() {
		if c.Name == "__Host-sid" {
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}
