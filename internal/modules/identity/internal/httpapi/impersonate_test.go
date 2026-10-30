package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// impersonate opens a support session in storeID and exchanges its code
// for the cookie, as the dashboard does.
func (f fixture) impersonate(t *testing.T, storeID string, admin ulid.ID, d time.Duration, write bool) string {
	t.Helper()
	g, err := f.id.Impersonate(f.inStore(t, storeID), admin, "ticket 42", d, write)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(g.CodeExpiresAt) > time.Minute || g.ExpiresAt.Sub(time.Now()) > d {
		t.Fatalf("grant = %+v", g)
	}
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/impersonation", `{"code":"`+g.Code+`"}`, sameOrigin))
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("exchange = %d %v", res.StatusCode, m)
	}
	var token string
	for _, c := range res.Cookies() {
		if c.Name == "__Host-sid" {
			token = c.Value
			if c.MaxAge <= 0 || time.Duration(c.MaxAge)*time.Second > d {
				t.Fatalf("cookie max-age = %d, session %v", c.MaxAge, d)
			}
		}
	}
	if token == "" {
		t.Fatal("no session cookie")
	}
	// The code works once.
	if res, _ := f.do(t, newReq(t, http.MethodPost, "/v1/auth/impersonation", `{"code":"`+g.Code+`"}`, sameOrigin)); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second exchange = %d", res.StatusCode)
	}
	return token
}

func (f fixture) inStore(t *testing.T, storeID string) context.Context {
	t.Helper()
	id, err := ulid.Parse(storeID)
	if err != nil {
		t.Fatal(err)
	}
	return tenant.WithStore(t.Context(), tenant.StoreID{ID: id})
}

// auditEntries returns the store's log, newest first, read by its owner.
func (f fixture) auditEntries(t *testing.T, owner account) []map[string]any {
	t.Helper()
	res, m := f.do(t, newReq(t, http.MethodGet, "/v1/stores/"+owner.storeID+"/audit-log?limit=200", "", bearer(owner.token)))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("audit log = %d %v", res.StatusCode, m)
	}
	var out []map[string]any
	for _, x := range m["items"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

// TestImpersonationIsAuditedAndScoped: P7 acceptance "impersonation
// always audited" — the start, every request (served or refused) and every
// change are in the store's log with the admin's ID; the session opens
// its one store only and is read-only by default.
func TestImpersonationIsAuditedAndScoped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.signup(t, "mobile")
	b := f.signup(t, "mobile")
	f.addMember(t, b.storeID, a.userID, "manager") // A's owner also works in B
	admin := f.ids.New()

	token := f.impersonate(t, a.storeID, admin, 10*time.Minute, false)
	storeA, storeB := "/v1/stores/"+a.storeID, "/v1/stores/"+b.storeID

	_, me := f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(token)))
	imp, _ := me["impersonation"].(map[string]any)
	ms := me["memberships"].([]any)
	if imp == nil || imp["read_only"] != true || len(ms) != 1 || ms[0].(map[string]any)["store_id"] != a.storeID {
		t.Fatalf("me = %v", me)
	}
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"reads its store", http.MethodGet, storeA, "", http.StatusOK},
		{"never another store of the owner", http.MethodGet, storeB, "", http.StatusNotFound},
		{"read-only refuses a change", http.MethodPatch, storeA, `{"name":"x","locale":"ar","timezone":"UTC"}`, http.StatusForbidden},
		{"never changes the account", http.MethodPost, "/v1/me/phone", `{"phone":"+201001234567"}`, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res, m := f.do(t, newReq(t, tt.method, tt.path, tt.body, cookie(token), sameOrigin)); res.StatusCode != tt.status {
				t.Fatalf("%s %s = %d %v; want %d", tt.method, tt.path, res.StatusCode, m, tt.status)
			}
		})
	}

	// A write session's change is attributed to the owner and names the admin.
	writer := f.impersonate(t, a.storeID, admin, 5*time.Minute, true)
	if res, m := f.do(t, newReq(t, http.MethodPatch, storeA, `{"name":"fixed","locale":"ar","timezone":"UTC"}`, cookie(writer), sameOrigin)); res.StatusCode != http.StatusOK {
		t.Fatalf("write session change = %d %v", res.StatusCode, m)
	}

	var started, served, refused, changed int
	for _, e := range f.auditEntries(t, a) {
		meta, _ := e["metadata"].(map[string]any)
		by := meta["impersonated_by"]
		switch e["action"] {
		case "impersonation.started":
			if by != admin.String() || meta["reason"] != "ticket 42" {
				t.Fatalf("start entry = %v", e)
			}
			started++
		case "impersonation.request":
			if by != admin.String() {
				t.Fatalf("request entry = %v", e)
			}
			if meta["refused"] == true {
				refused++
			} else {
				served++
			}
		case "store.updated":
			if by != admin.String() || e["actor_user_id"] != a.userID {
				t.Fatalf("change entry = %v", e)
			}
			changed++
		}
	}
	// Requests in A's store and on the account: the read and the write
	// session's change served, the read-only change and phone refused.
	// The 404 in B is recorded nowhere: it never reached a store.
	if started != 2 || served != 3 || refused != 2 || changed != 1 {
		t.Fatalf("started=%d served=%d refused=%d changed=%d", started, served, refused, changed)
	}
	// Nothing was written to B's log.
	for _, e := range f.auditEntries(t, b) {
		if e["action"] != "store.created" {
			t.Fatalf("B's log = %v", e)
		}
	}
}

// TestImpersonationExpires: P7 acceptance — the session is never extended
// by use and ends at its deadline.
func TestImpersonationExpires(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.signup(t, "mobile")
	admin := f.ids.New()
	token := f.impersonate(t, a.storeID, admin, 5*time.Minute, false)

	// Long idle: an ordinary session would slide its expiry on use.
	if _, err := f.owner.Exec(t.Context(), "UPDATE user_sessions SET last_seen_at = now() - interval '1 hour' WHERE impersonator_id = $1", admin); err != nil {
		t.Fatal(err)
	}
	var before, after time.Time
	if err := f.owner.QueryRow(t.Context(), "SELECT expires_at FROM user_sessions WHERE impersonator_id = $1", admin).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if res, _ := f.do(t, newReq(t, http.MethodGet, "/v1/stores/"+a.storeID, "", cookie(token))); res.StatusCode != http.StatusOK {
		t.Fatalf("live session = %d", res.StatusCode)
	}
	if err := f.owner.QueryRow(t.Context(), "SELECT expires_at FROM user_sessions WHERE impersonator_id = $1", admin).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) || time.Until(after) > 5*time.Minute {
		t.Fatalf("expiry moved: %v -> %v", before, after)
	}

	if _, err := f.owner.Exec(t.Context(), "UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE impersonator_id = $1", admin); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/stores/" + a.storeID, "/v1/me"} {
		if res, _ := f.do(t, newReq(t, http.MethodGet, path, "", cookie(token))); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expired session on %s = %d", path, res.StatusCode)
		}
	}
}

func TestImpersonationValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.signup(t, "mobile")
	ctx := f.inStore(t, a.storeID)
	for _, d := range []time.Duration{0, 4 * time.Minute, 61 * time.Minute} {
		if _, err := f.id.Impersonate(ctx, f.ids.New(), "ticket", d, false); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%v: %v", d, err)
		}
	}
	if _, err := f.id.Impersonate(f.inStore(t, f.ids.New().String()), f.ids.New(), "ticket", 10*time.Minute, false); !errs.Is(err, errs.KindConflict) {
		t.Errorf("store without owner: %v", err)
	}
	for _, code := range []string{"", "junk", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if res, _ := f.do(t, newReq(t, http.MethodPost, "/v1/auth/impersonation", `{"code":"`+code+`"}`, sameOrigin)); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("code %q = %d", code, res.StatusCode)
		}
	}
}
