package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	infra       testinfra.Infra
	crossTenant testkit.Registry
)

// TestMain fails the package if an identity operation under
// /v1/stores/{store_id} lacks a cross-tenant test.
func TestMain(m *testing.M) {
	code := testinfra.Run(m, &infra)
	if code == 0 && runAll() {
		_, file, _, _ := runtime.Caller(0)
		spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "api", "openapi.yaml"))
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "load spec:", err)
			os.Exit(1)
		}
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "identity")); len(missing) > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "operations without a cross-tenant test:", missing)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

func runAll() bool {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.run=") || strings.HasPrefix(a, "-test.fuzz=") {
			return false
		}
	}
	return true
}

type ipKey struct{}

type fixture struct {
	h     http.Handler
	owner *pgxpool.Pool
	ids   *ulid.Generator
}

// newFixture mounts the real API: identity's routes and tenancy's routes
// behind identity's Scope middleware and policy, as cmd/platform does.
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, err := pgxpool.New(ctx, infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ten, err := tenancy.New(tenancy.Deps{Pool: pool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, Audit: identity.NewAuditRecorder(ids),
		Config: config.Tenancy{BaseDomain: "iftahonline.com", HostCacheSize: 10, HostCacheTTL: time.Minute,
			NegativeCacheSize: 10, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	limits := app.Limits{
		SignupPerIP:      ratelimit.Limit{Burst: 50, Period: time.Hour},
		LoginPerIP:       ratelimit.Limit{Burst: 50, Period: time.Hour},
		LoginFailPerUser: ratelimit.Limit{Burst: 2, Period: time.Hour},
	}
	id, err := identity.New(identity.Deps{
		Pool: pool, Redis: rdb, Tenancy: ten.Service(), IDs: ids, Log: log, Limits: &limits,
		Config: config.Identity{Argon2MemoryKiB: 8 * 1024, Argon2Time: 1, Argon2Threads: 1, HashConcurrency: 4},
		ClientIP: func(ctx context.Context) (netip.Addr, bool) {
			ip, ok := ctx.Value(ipKey{}).(netip.Addr)
			return ip, ok
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	resp := problem.NewResponder(log)
	id.Mount(mux, resp)
	ten.Mount(mux, id.Authorizer(), id.Scope(), resp)

	// Each test request comes from its own IP (per-IP limits).
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := ids.New()
		ip := netip.AddrFrom4([4]byte{10, b[13], b[14], b[15]})
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ipKey{}, ip)))
	})
	return fixture{h: h, owner: owner, ids: ids}
}

func (f fixture) unique(p string) string { return p + strings.ToLower(f.ids.New().String()[16:]) }

type reqOpt func(*http.Request)

func cookie(v string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-sid", Value: v}) }
}
func bearer(v string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+v) }
}
func sameOrigin(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }
func crossSite(r *http.Request)  { r.Header.Set("Sec-Fetch-Site", "cross-site") }
func origin(o string) reqOpt     { return func(r *http.Request) { r.Header.Set("Origin", o) } }

func newReq(t *testing.T, method, path, body string, opts ...reqOpt) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(t.Context(), method, "http://api.iftahonline.com"+path, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// result is a response with its body already read and closed.
type result struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r result) Cookies() []*http.Cookie { return r.cookies }

func (f fixture) do(t *testing.T, r *http.Request) (result, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	raw := rec.Result()
	res := result{StatusCode: raw.StatusCode, Header: raw.Header, cookies: raw.Cookies()}
	_ = raw.Body.Close()
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return res, m
}

type account struct {
	email, token, userID, storeID string
}

func (f fixture) signup(t *testing.T, client string) account {
	t.Helper()
	email := f.unique("u") + "@example.com"
	body := fmt.Sprintf(`{"email":%q,"password":"correct horse battery","name":"Ahmed","client":%q,
		"store":{"name":"Store","slug":%q,"currency":"EGP","locale":"ar","timezone":"Africa/Cairo"}}`, email, client, f.unique("s-"))
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/signup", body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("signup = %d %v", res.StatusCode, m)
	}
	acc := account{email: email, userID: m["user"].(map[string]any)["id"].(string), storeID: m["store_id"].(string)}
	if client == "mobile" {
		acc.token = m["token"].(string)
	} else {
		for _, c := range res.Cookies() {
			if c.Name == "__Host-sid" {
				acc.token = c.Value
			}
		}
	}
	if acc.token == "" {
		t.Fatal("no session token")
	}
	return acc
}

func TestSignupAndLoginCookies(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Web: the token is only in a hardened cookie, never in the body.
	email := f.unique("w") + "@example.com"
	res, m := f.do(t, newReq(t, http.MethodPost, "/v1/auth/signup", fmt.Sprintf(`{"email":%q,"password":"correct horse battery","name":"Web",
		"store":{"name":"S","slug":%q,"currency":"SAR","locale":"en","timezone":"Asia/Riyadh"}}`, email, f.unique("w-"))))
	if res.StatusCode != http.StatusCreated || m["token"] != nil || m["store_id"] == nil {
		t.Fatalf("web signup = %d %v", res.StatusCode, m)
	}
	set := res.Header.Get("Set-Cookie")
	for _, attr := range []string{"__Host-sid=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age="} {
		if !strings.Contains(set, attr) {
			t.Fatalf("Set-Cookie %q lacks %q", set, attr)
		}
	}
	if strings.Contains(strings.ToLower(set), "domain=") {
		t.Fatal("session cookie must not set Domain")
	}

	// Mobile: token in the body, no cookie.
	mob := f.signup(t, "mobile")
	res, m = f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"correct horse battery","client":"mobile"}`, mob.email)))
	if res.StatusCode != http.StatusOK || m["token"] == nil || res.Header.Get("Set-Cookie") != "" {
		t.Fatalf("mobile login = %d %v %q", res.StatusCode, m, res.Header.Get("Set-Cookie"))
	}

	// Web login sets a cookie.
	res, _ = f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"correct horse battery"}`, email)))
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Set-Cookie"), "__Host-sid=") {
		t.Fatalf("web login = %d", res.StatusCode)
	}
}

func TestAuthValidationAndErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")
	tests := []struct {
		name   string
		r      *http.Request
		status int
		code   string
	}{
		{"signup invalid fields", newReq(t, http.MethodPost, "/v1/auth/signup", `{"email":"x","password":"short","name":"","store":{"name":"","slug":"a","currency":"EGP","locale":"ar","timezone":"UTC"}}`), http.StatusUnprocessableEntity, "validation_failed"},
		{"signup malformed json", newReq(t, http.MethodPost, "/v1/auth/signup", `{"email":`), http.StatusBadRequest, ""},
		{"login wrong password", newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"nope nope nope"}`, acc.email)), http.StatusUnauthorized, ""},
		{"login unknown email", newReq(t, http.MethodPost, "/v1/auth/login", `{"email":"nobody@example.com","password":"nope nope nope"}`), http.StatusUnauthorized, ""},
		{"me without session", newReq(t, http.MethodGet, "/v1/me", ""), http.StatusUnauthorized, ""},
		{"me with junk cookie", newReq(t, http.MethodGet, "/v1/me", "", cookie("junk")), http.StatusUnauthorized, ""},
		{"revoke malformed id", newReq(t, http.MethodDelete, "/v1/me/sessions/nope", "", bearer(acc.token)), http.StatusNotFound, "session_not_found"},
		{"audit bad cursor", newReq(t, http.MethodGet, "/v1/stores/"+acc.storeID+"/audit-log?cursor=!!", "", bearer(acc.token)), http.StatusUnprocessableEntity, "validation_failed"},
		{"audit limit too large", newReq(t, http.MethodGet, "/v1/stores/"+acc.storeID+"/audit-log?limit=999", "", bearer(acc.token)), http.StatusUnprocessableEntity, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, m := f.do(t, tt.r)
			if res.StatusCode != tt.status || (tt.code != "" && m["code"] != tt.code) {
				t.Fatalf("= %d %v; want %d %q", res.StatusCode, m, tt.status, tt.code)
			}
			if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/problem+json") {
				t.Fatalf("content type %q", res.Header.Get("Content-Type"))
			}
		})
	}

}

func TestLoginLockoutOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")
	// Repeated failures lock the account with 429 + Retry-After.
	for range 2 {
		f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"wrong wrong wrong"}`, acc.email)))
	}
	res, _ := f.do(t, newReq(t, http.MethodPost, "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"correct horse battery"}`, acc.email)))
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("lockout = %d %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
}

func TestMeSessionsAndLogout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")

	res, m := f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(acc.token)))
	ms, _ := m["memberships"].([]any)
	if res.StatusCode != http.StatusOK || len(ms) != 1 || ms[0].(map[string]any)["role"] != "owner" || ms[0].(map[string]any)["store_id"] != acc.storeID {
		t.Fatalf("me = %d %v", res.StatusCode, m)
	}

	res, m = f.do(t, newReq(t, http.MethodGet, "/v1/me/sessions", "", cookie(acc.token)))
	items, _ := m["items"].([]any)
	if res.StatusCode != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["current"] != true {
		t.Fatalf("sessions = %d %v", res.StatusCode, m)
	}
	sid := items[0].(map[string]any)["id"].(string)

	// Another user cannot revoke it (404, not 403).
	other := f.signup(t, "mobile")
	if res, _ = f.do(t, newReq(t, http.MethodDelete, "/v1/me/sessions/"+sid, "", bearer(other.token))); res.StatusCode != http.StatusNotFound {
		t.Fatalf("revoke other's session = %d", res.StatusCode)
	}

	// Logout clears the cookie and kills the session.
	res, _ = f.do(t, newReq(t, http.MethodPost, "/v1/auth/logout", "", cookie(acc.token), sameOrigin))
	if res.StatusCode != http.StatusNoContent || !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout = %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	if res, _ := f.do(t, newReq(t, http.MethodGet, "/v1/me", "", cookie(acc.token))); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout = %d", res.StatusCode)
	}
	// Revoking by ID works for the owner of the session.
	login := f.signup(t, "mobile")
	_, m = f.do(t, newReq(t, http.MethodGet, "/v1/me/sessions", "", bearer(login.token)))
	lsid := m["items"].([]any)[0].(map[string]any)["id"].(string)
	if res, _ := f.do(t, newReq(t, http.MethodDelete, "/v1/me/sessions/"+lsid, "", bearer(login.token))); res.StatusCode != http.StatusNoContent {
		t.Fatalf("self revoke = %d", res.StatusCode)
	}
}

// TestCSRF: a cookie-authenticated state change must come from our origin.
func TestCSRF(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc := f.signup(t, "web")
	patch := `{"name":"x","locale":"ar","timezone":"UTC"}`
	path := "/v1/stores/" + acc.storeID
	tests := []struct {
		name   string
		r      *http.Request
		status int
	}{
		{"no origin info", newReq(t, http.MethodPatch, path, patch, cookie(acc.token)), http.StatusForbidden},
		{"cross-site fetch", newReq(t, http.MethodPatch, path, patch, cookie(acc.token), crossSite), http.StatusForbidden},
		{"foreign Origin", newReq(t, http.MethodPatch, path, patch, cookie(acc.token), origin("https://evil.example.com")), http.StatusForbidden},
		{"a store subdomain is foreign", newReq(t, http.MethodPatch, path, patch, cookie(acc.token), origin("https://shop.iftahonline.com")), http.StatusForbidden},
		{"same-origin fetch", newReq(t, http.MethodPatch, path, patch, cookie(acc.token), sameOrigin), http.StatusOK},
		{"matching Origin", newReq(t, http.MethodPatch, path, patch, cookie(acc.token), origin("http://api.iftahonline.com")), http.StatusOK},
		{"GET needs no origin", newReq(t, http.MethodGet, path, "", cookie(acc.token)), http.StatusOK},
		{"bearer needs no origin", newReq(t, http.MethodPatch, path, patch, bearer(acc.token), crossSite), http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res, m := f.do(t, tt.r); res.StatusCode != tt.status {
				t.Fatalf("= %d %v; want %d", res.StatusCode, m, tt.status)
			}
		})
	}
}

// addMember gives userID a role in storeID directly (invitations are P1b).
func (f fixture) addMember(t *testing.T, storeID, userID, role string) {
	t.Helper()
	sid, _ := ulid.Parse(storeID)
	uid, _ := ulid.Parse(userID)
	err := pgx.BeginFunc(t.Context(), f.owner, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), "SELECT set_config('app.store_id', $1::uuid::text, true)", sid); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), "INSERT INTO store_memberships (store_id, user_id, role) VALUES ($1, $2, $3)", sid, uid, role)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRolesThroughRealMiddleware: the matrix applies end to end.
func TestRolesThroughRealMiddleware(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := f.signup(t, "mobile")
	staff := map[string]account{}
	for _, role := range []string{"manager", "support", "shipping"} {
		a := f.signup(t, "mobile")
		f.addMember(t, owner.storeID, a.userID, role)
		staff[role] = a
	}
	store := "/v1/stores/" + owner.storeID
	tests := []struct {
		who    account
		method string
		path   string
		body   string
		status int
	}{
		{owner, http.MethodGet, store + "/audit-log", "", http.StatusOK},
		{staff["manager"], http.MethodGet, store + "/audit-log", "", http.StatusOK},
		{staff["manager"], http.MethodPatch, store, `{"name":"m","locale":"ar","timezone":"UTC"}`, http.StatusOK},
		{staff["support"], http.MethodGet, store + "/audit-log", "", http.StatusForbidden},
		{staff["support"], http.MethodGet, store + "/domains", "", http.StatusOK},
		{staff["support"], http.MethodPatch, store, `{"name":"s","locale":"ar","timezone":"UTC"}`, http.StatusForbidden},
		{staff["shipping"], http.MethodGet, store, "", http.StatusOK},
		{staff["shipping"], http.MethodGet, store + "/domains", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		if res, m := f.do(t, newReq(t, tt.method, tt.path, tt.body, bearer(tt.who.token))); res.StatusCode != tt.status {
			t.Errorf("%s %s = %d %v; want %d", tt.method, tt.path, res.StatusCode, m, tt.status)
		}
	}

	// /v1/me lists the staff member's store with the right role.
	_, m := f.do(t, newReq(t, http.MethodGet, "/v1/me", "", bearer(staff["support"].token)))
	ms := m["memberships"].([]any)
	found := false
	for _, x := range ms {
		e := x.(map[string]any)
		found = found || (e["store_id"] == owner.storeID && e["role"] == "support")
	}
	if !found {
		t.Fatalf("support's memberships = %v", ms)
	}

	// The manager's change is in the audit log, attributed to them.
	_, m = f.do(t, newReq(t, http.MethodGet, store+"/audit-log?limit=1", "", bearer(owner.token)))
	items := m["items"].([]any)
	e := items[0].(map[string]any)
	if e["action"] != "store.updated" || e["actor_user_id"] != staff["manager"].userID || m["next_cursor"] == nil {
		t.Fatalf("latest audit entry = %v (page %v)", e, m)
	}
}

// TestCrossTenant: A's owner, with real sessions and RLS, cannot reach B.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.signup(t, "mobile"), f.signup(t, "mobile")
	// Give B an audit trail worth leaking.
	f.do(t, newReq(t, http.MethodPatch, "/v1/stores/"+b.storeID, `{"name":"Secret B","locale":"ar","timezone":"UTC"}`, bearer(b.token)))

	testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, testkit.CrossTenantCase{
		OperationID: "listAuditLog",
		Request:     newReq(t, http.MethodGet, "/v1/stores/"+b.storeID+"/audit-log", "", bearer(a.token)),
		Leaks:       []string{"Secret B", b.userID},
	})
	// The tenancy routes behind the real middleware behave the same.
	for _, path := range []string{"", "/domains"} {
		res, m := f.do(t, newReq(t, http.MethodGet, "/v1/stores/"+b.storeID+path, "", bearer(a.token)))
		if res.StatusCode != http.StatusNotFound || strings.Contains(fmt.Sprint(m), "Secret B") {
			t.Fatalf("A reads B%s = %d %v", path, res.StatusCode, m)
		}
	}
	res, _ := f.do(t, newReq(t, http.MethodPatch, "/v1/stores/"+b.storeID, `{"name":"pwned","locale":"ar","timezone":"UTC"}`, bearer(a.token)))
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("A writes B = %d", res.StatusCode)
	}
	_, m := f.do(t, newReq(t, http.MethodGet, "/v1/stores/"+b.storeID, "", bearer(b.token)))
	if m["name"] != "Secret B" {
		t.Fatalf("B's store changed: %v", m)
	}
	// No session at all is 401, a malformed store ID is 404.
	if res, _ := f.do(t, newReq(t, http.MethodGet, "/v1/stores/"+b.storeID, "")); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", res.StatusCode)
	}
	if res, _ := f.do(t, newReq(t, http.MethodGet, "/v1/stores/not-an-id", "", bearer(a.token))); res.StatusCode != http.StatusNotFound {
		t.Fatalf("malformed store id = %d", res.StatusCode)
	}
}
