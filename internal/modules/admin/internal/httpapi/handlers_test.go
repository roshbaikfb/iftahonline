package httpapi_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

const (
	adminHost = "admin.iftahonline.com"
	adminPW   = "a long admin passphrase"
)

type fixture struct {
	h       http.Handler
	mod     *admin.Module
	ten     *tenancy.Module
	appPool *pgxpool.Pool
	ids     *ulid.Generator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	open := func(url string) *pgxpool.Pool {
		p, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	adminPool, appPool := open(infra.AdminURL), open(infra.AppURL)
	opts, _ := redis.ParseURL(infra.RedisURL)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, _ := river.NewClient(riverpgxv5.New(appPool), &river.Config{Middleware: jobs.Middleware()})
	ids := ulid.NewGenerator()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ten, err := tenancy.New(tenancy.Deps{Pool: appPool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, AdminPool: adminPool,
		Config: config.Tenancy{BaseDomain: "iftahonline.com", HostCacheSize: 10, HostCacheTTL: time.Minute,
			NegativeCacheSize: 10, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=")
	ent, err := entitlements.New(entitlements.Deps{Pool: appPool, Source: entitlements.Fixed{entitlements.Coupons: {Value: 0}}, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	mod, err := admin.New(admin.Deps{AdminPool: adminPool, Redis: rdb, Directory: ten.Directory(), Stores: ten.Service(),
		IDs: ids, Log: log, Secrets: config.Secrets{Keyring: keyring}, AdminHost: adminHost,
		Impersonate: func(_ context.Context, _ ulid.ID, _ string, d time.Duration, _ bool) (admin.Impersonation, error) {
			return admin.Impersonation{Code: "c0de", CodeExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(d)}, nil
		},
		DashboardURL: "https://app.iftahonline.com",
		Plans:        billing.NewCatalog(adminPool, ids), Enforced: entitlements.Enforced, Overrides: ent.Overrides(),
		Hashing: config.Identity{Argon2MemoryKiB: 8 * 1024, Argon2Time: 1, Argon2Threads: 1, HashConcurrency: 2}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := mod.Mount(mux, problem.NewResponder(log)); err != nil {
		t.Fatal(err)
	}
	return fixture{h: mux, mod: mod, ten: ten, appPool: appPool, ids: ids}
}

type resp struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
	body    map[string]any
}

func (f fixture) do(t *testing.T, method, host, path, body string, mods ...func(*http.Request)) resp {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(t.Context(), method, "https://"+host+path, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, m := range mods {
		m(r)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	out := resp{status: res.StatusCode, header: res.Header, cookies: res.Cookies()}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
	}
	return out
}

func sameOrigin(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }

func withCookie(v string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-asid", Value: v}) }
}

// login creates an admin and signs in, returning the session cookie.
func (f fixture) login(t *testing.T) string {
	t.Helper()
	email := "ops-" + strings.ToLower(f.ids.New().String()[16:]) + "@iftahonline.com"
	c, err := f.mod.CreateAdmin(t.Context(), email, "Ops", adminPW)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(c.Secret)
	body := fmt.Sprintf(`{"email":%q,"password":%q,"code":%q}`, email, adminPW, totp.HOTP(raw, totp.Step(time.Now()), 6))
	r := f.do(t, http.MethodPost, adminHost, "/admin/v1/auth/login", body, sameOrigin)
	if r.status != http.StatusOK {
		t.Fatalf("login = %d %v", r.status, r.body)
	}
	set := r.header.Get("Set-Cookie")
	for _, attr := range []string{"__Host-asid=", "HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(set, attr) {
			t.Fatalf("cookie %q lacks %s", set, attr)
		}
	}
	return r.cookies[0].Value
}

func (f fixture) store(t *testing.T) string {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	err := postgres.InTenantTx(ctx, f.appPool, func(tx pgx.Tx) error {
		_, err := f.ten.Service().CreateStoreTx(ctx, tx, tenancy.NewStore{Slug: "con-" + strings.ToLower(f.ids.New().String()[16:]),
			Name: "Console target", Currency: "SAR", Locale: "en", Timezone: "Asia/Riyadh"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// TestOnlyOnAdminHost: every console route, login included, is a plain
// 404 on any other host.
func TestOnlyOnAdminHost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	for _, host := range []string{"api.iftahonline.com", "shop.iftahonline.com", "evil.example.com", "admin.iftahonline.com.evil.com"} {
		for _, path := range []string{"/admin/v1/auth/login", "/admin/v1/me", "/admin/v1/stores"} {
			method := http.MethodGet
			if strings.HasSuffix(path, "login") {
				method = http.MethodPost
			}
			r := f.do(t, method, host, path, `{}`, sameOrigin, withCookie(sess))
			if r.status != http.StatusNotFound || r.body["code"] != "route_not_found" {
				t.Errorf("%s %s%s = %d %v", method, host, path, r.status, r.body)
			}
		}
	}
	// With a port, the admin host still matches.
	if r := f.do(t, http.MethodGet, adminHost+":443", "/admin/v1/me", "", withCookie(sess)); r.status != http.StatusOK {
		t.Fatalf("admin host with port = %d", r.status)
	}
}

func TestConsoleFlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	storeID := f.store(t)

	r := f.do(t, http.MethodGet, adminHost, "/admin/v1/me", "", withCookie(sess))
	if r.status != http.StatusOK || r.header.Get("X-Robots-Tag") == "" {
		t.Fatalf("me = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/stores/"+storeID, "", withCookie(sess))
	if r.status != http.StatusOK || len(r.body["domains"].([]any)) != 1 {
		t.Fatalf("store = %d %v", r.status, r.body)
	}
	slug := r.body["store"].(map[string]any)["slug"].(string)
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/stores?query="+slug, "", withCookie(sess))
	if items, _ := r.body["items"].([]any); r.status != http.StatusOK || len(items) != 1 {
		t.Fatalf("search = %d %v", r.status, r.body)
	}

	// Writes need same-origin; then suspend and reactivate.
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/stores/"+storeID+"/suspend", `{"reason":"abuse report"}`, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site suspend = %d", r.status)
	}
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/stores/"+storeID+"/suspend", `{"reason":"abuse report"}`, withCookie(sess), sameOrigin)
	if r.status != http.StatusOK || r.body["status"] != "suspended" {
		t.Fatalf("suspend = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/stores/"+storeID+"/suspend", `{"reason":"abuse report"}`, withCookie(sess), sameOrigin); r.status != http.StatusConflict {
		t.Fatalf("suspend twice = %d", r.status)
	}
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/stores/"+storeID+"/reactivate", `{"reason":"cleared"}`, withCookie(sess), sameOrigin)
	if r.status != http.StatusOK || r.body["status"] != "active" {
		t.Fatalf("reactivate = %d %v", r.status, r.body)
	}
	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/admin/v1/stores/not-an-id", "", http.StatusNotFound},
		{http.MethodGet, "/admin/v1/stores/" + f.ids.New().String(), "", http.StatusNotFound},
		{http.MethodPost, "/admin/v1/stores/" + storeID + "/suspend", `{"reason":"x"}`, http.StatusUnprocessableEntity},
		{http.MethodGet, "/admin/v1/stores?status=gone", "", http.StatusUnprocessableEntity},
		{http.MethodGet, "/admin/v1/audit-log?cursor=!!", "", http.StatusUnprocessableEntity},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}

	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/audit-log?limit=5", "", withCookie(sess))
	if items, _ := r.body["items"].([]any); r.status != http.StatusOK || len(items) == 0 {
		t.Fatalf("audit = %d %v", r.status, r.body)
	}

	// Logout clears the cookie and ends the session.
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/auth/logout", "", withCookie(sess), sameOrigin)
	if r.status != http.StatusNoContent || !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout = %d %q", r.status, r.header.Get("Set-Cookie"))
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/me", "", withCookie(sess)); r.status != http.StatusUnauthorized {
		t.Fatalf("after logout = %d", r.status)
	}
}

func TestConsoleRefusesWithoutAdminSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for name, mods := range map[string][]func(*http.Request){
		"no cookie":         nil,
		"junk cookie":       {withCookie("junk")},
		"merchant cookie":   {func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-sid", Value: strings.Repeat("A", 43)}) }},
		"bearer, no cookie": {func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43)) }},
	} {
		if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/stores", "", mods...); r.status != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, r.status)
		}
	}
	if r := f.do(t, http.MethodPost, adminHost, "/admin/v1/auth/login", `{"email":"x@y.co","password":"nope","code":"000000"}`, sameOrigin); r.status != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", r.status)
	}
	if r := f.do(t, http.MethodPost, adminHost, "/admin/v1/auth/login", `{`, sameOrigin); r.status != http.StatusBadRequest {
		t.Fatalf("malformed login = %d", r.status)
	}
}

func TestConsoleImpersonate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	path := "/admin/v1/stores/" + f.store(t) + "/impersonate"

	if r := f.do(t, http.MethodPost, adminHost, path, `{"reason":"ticket 9"}`, sameOrigin); r.status != http.StatusUnauthorized {
		t.Fatalf("no session = %d", r.status)
	}
	if r := f.do(t, http.MethodPost, adminHost, path, `{"reason":"ticket 9"}`, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r := f.do(t, http.MethodPost, adminHost, path, `{"reason":"ticket 9","minutes":15}`, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["code"] != "c0de" || r.body["url"] != "https://app.iftahonline.com/impersonate#code=c0de" {
		t.Fatalf("impersonate = %d %v", r.status, r.body)
	}
	for _, body := range []string{`{"reason":"x"}`, `{"reason":"ticket 9","minutes":0}`, `{"reason":"ticket 9","minutes":61}`} {
		if r := f.do(t, http.MethodPost, adminHost, path, body, withCookie(sess), sameOrigin); r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %v", body, r.status, r.body)
		}
	}
	if r := f.do(t, http.MethodPost, adminHost, path, ``, withCookie(sess), sameOrigin); r.status != http.StatusBadRequest {
		t.Errorf("no body = %d", r.status)
	}
}

func TestConsolePlanCatalog(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	suffix := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0')
		}
		return r
	}, strings.ToLower(f.ids.New().String()[16:]))
	key, code := "t_"+suffix, "t_"+suffix

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/plans", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	feature := `{"kind":"flag","name_ar":"ميزة","name_en":"Perk","sort":900,"reason":"sales request"}`
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/features/"+key, feature, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r := f.do(t, http.MethodPut, adminHost, "/admin/v1/features/"+key, feature, withCookie(sess), sameOrigin)
	if r.status != http.StatusOK || r.body["enforced"] != false || r.body["key"] != key {
		t.Fatalf("save feature = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/features", "", withCookie(sess))
	if items, _ := r.body["items"].([]any); r.status != http.StatusOK || len(items) < 11 {
		t.Fatalf("features = %d %v", r.status, r.body)
	}

	plan := fmt.Sprintf(`{"code":%q,"kind":"subscription","name_ar":"نمو","name_en":"Growth","trial_days":7,
		"prices":[{"currency":"EGP","cycle":"monthly","amount_minor":9900},{"currency":"SAR","cycle":"monthly","amount_minor":1900},
		          {"currency":"AED","cycle":"monthly","amount_minor":1900}],
		"grants":[{"feature":%q,"value":1},{"feature":"products","unlimited":true}],"reason":"launch"}`, code, key)
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/plans", plan, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["version"] != float64(1) || r.body["active"] != true || len(r.body["grants"].([]any)) != 2 {
		t.Fatalf("publish = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/plans", "", withCookie(sess))
	if items, _ := r.body["items"].([]any); r.status != http.StatusOK || len(items) < 7 {
		t.Fatalf("plans = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, "/admin/v1/plans/"+code+"/retire", `{"reason":"done"}`, withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("retire = %d %v", r.status, r.body)
	}

	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/admin/v1/features/" + key, `{"kind":"count","name_ar":"م","name_en":"P","reason":"change kind"}`, http.StatusConflict},
		{http.MethodPost, "/admin/v1/plans", strings.Replace(plan, `{"feature":"products","unlimited":true}`, `{"feature":"products","value":1},{"feature":"products","value":2}`, 1), http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/plans", strings.Replace(plan, `"value":1}`, `"value":1},{"feature":"no_such_key","value":1}`, 1), http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/plans", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/plans/free/retire", `{"reason":"never"}`, http.StatusConflict},
		{http.MethodPost, "/admin/v1/plans/" + code + "/retire", `{"reason":"again"}`, http.StatusNotFound},
		{http.MethodPost, "/admin/v1/plans/" + code + "/retire", ``, http.StatusBadRequest},
		{http.MethodPut, "/admin/v1/features/" + key, ``, http.StatusBadRequest},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
}

func TestConsoleStoreOverrides(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	path := "/admin/v1/stores/" + f.store(t) + "/entitlements"

	if r := f.do(t, http.MethodGet, adminHost, path, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	r := f.do(t, http.MethodGet, adminHost, path, "", withCookie(sess))
	if r.status != http.StatusOK || len(r.body["overrides"].([]any)) != 0 {
		t.Fatalf("get = %d %v", r.status, r.body)
	}
	set := `{"value":1,"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","reason":"promo month"}`
	if r = f.do(t, http.MethodPut, adminHost, path+"/coupons", set, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r = f.do(t, http.MethodPut, adminHost, path+"/coupons", set, withCookie(sess), sameOrigin)
	ov, _ := r.body["overrides"].([]any)
	if r.status != http.StatusOK || len(ov) != 1 || ov[0].(map[string]any)["active"] != true || ov[0].(map[string]any)["expires_at"] == nil {
		t.Fatalf("set = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPut, adminHost, path+"/products", `{"unlimited":true,"reason":"big import"}`, withCookie(sess), sameOrigin); r.status != http.StatusOK {
		t.Fatalf("unlimited = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodPost, adminHost, path+"/coupons/clear", `{"reason":"promo over"}`, withCookie(sess), sameOrigin)
	if ov, _ = r.body["overrides"].([]any); r.status != http.StatusOK || len(ov) != 1 {
		t.Fatalf("clear = %d %v", r.status, r.body)
	}
	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, path + "/coupons", `{"value":2,"reason":"promo"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, path + "/live_chat", `{"value":1,"reason":"promo"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, path + "/coupons", `{"value":1,"expires_at":"2020-01-01T00:00:00Z","reason":"promo"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, path + "/coupons", ``, http.StatusBadRequest},
		{http.MethodPost, path + "/coupons/clear", `{"reason":"again"}`, http.StatusNotFound},
		{http.MethodPost, path + "/coupons/clear", ``, http.StatusBadRequest},
		{http.MethodGet, "/admin/v1/stores/not-an-id/entitlements", "", http.StatusNotFound},
		{http.MethodPut, "/admin/v1/stores/not-an-id/entitlements/coupons", `{"value":1,"reason":"promo"}`, http.StatusNotFound},
		{http.MethodPost, "/admin/v1/stores/not-an-id/entitlements/coupons/clear", `{"reason":"promo"}`, http.StatusNotFound},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
}
