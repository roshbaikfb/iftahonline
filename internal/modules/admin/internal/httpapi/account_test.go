package httpapi_test

import (
	"net/http"
	"testing"
)

// TestConsoleOwnAccount (C3): any admin, signed in, renames themselves,
// lists and ends their sessions; changes need a same-origin request and
// are validated; a wrong current password is a field error.
func TestConsoleOwnAccount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	viewer := f.loginAs(t, "viewer")

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/me/sessions", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	if r := f.do(t, http.MethodPatch, adminHost, "/admin/v1/me", `{"name":"هالة"}`, withCookie(viewer)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	if r := f.do(t, http.MethodPatch, adminHost, "/admin/v1/me", `{"name":" "}`, withCookie(viewer), sameOrigin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("an empty name = %d %v", r.status, r.body)
	}
	if r := f.do(t, http.MethodPatch, adminHost, "/admin/v1/me", `{"name":"هالة"}`, withCookie(viewer), sameOrigin); r.status != http.StatusOK || r.body["name"] != "هالة" {
		t.Fatalf("rename = %d %v", r.status, r.body)
	}
	r := f.do(t, http.MethodGet, adminHost, "/admin/v1/me/sessions", "", withCookie(viewer))
	items, _ := r.body["items"].([]any)
	if r.status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["current"] != true {
		t.Fatalf("sessions = %d %v", r.status, r.body)
	}
	this, _ := items[0].(map[string]any)["id"].(string)
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/sessions/"+this+"/revoke", "", withCookie(viewer), sameOrigin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("revoke this device = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/sessions/"+f.ids.New().String()+"/revoke", "", withCookie(viewer), sameOrigin); r.status != http.StatusNotFound {
		t.Fatalf("revoke an unknown session = %d", r.status)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/sessions/revoke-others", "", withCookie(viewer), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("revoke others = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/password", `{"current_password":"wrong","new_password":"a brand new admin passphrase"}`,
		withCookie(viewer), sameOrigin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong current password = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/phone/start", `{"code":"000000"}`, withCookie(viewer), sameOrigin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong old code = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/me/phone/confirm", `{"code":"000000"}`, withCookie(viewer), sameOrigin); r.status != http.StatusConflict {
		t.Fatalf("confirm before starting = %d %v", r.status, r.body)
	}
}
