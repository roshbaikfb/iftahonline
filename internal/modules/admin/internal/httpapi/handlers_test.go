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

	"github.com/roshbaikfb/iftahonline/internal/modules/ads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
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
	h         http.Handler
	mod       *admin.Module
	ten       *tenancy.Module
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
	ids       *ulid.Generator
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
	bill, err := billing.New(billing.Deps{Pool: appPool,
		Stores: func(context.Context) (billing.StoreInfo, error) {
			return billing.StoreInfo{Name: "Test", Currency: "EGP"}, nil
		},
		Holds: func(context.Context, pgx.Tx, bool) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	adv, err := ads.New(ads.Deps{Pool: appPool, AdminPool: adminPool, IDs: ids, Log: log}) // no bucket: uploads are refused
	if err != nil {
		t.Fatal(err)
	}
	siteConsole, err := site.NewConsole(adminPool, nil, ids, log) // no bucket: uploads are refused
	if err != nil {
		t.Fatal(err)
	}
	mod, err := admin.New(admin.Deps{AdminPool: adminPool, Redis: rdb, Directory: ten.Directory(), Stores: ten.Service(),
		IDs: ids, Log: log, Secrets: config.Secrets{Keyring: keyring}, AdminHost: adminHost,
		Impersonate: func(_ context.Context, _ ulid.ID, _ string, d time.Duration, _ bool) (admin.Impersonation, error) {
			return admin.Impersonation{Code: "c0de", CodeExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(d)}, nil
		},
		DashboardURL: "https://app.iftahonline.com",
		Plans:        billing.NewCatalog(adminPool, ids), Enforced: entitlements.Enforced, Overrides: ent.Overrides(), Billing: bill.Console(), Agencies: bill.Agencies(), Ads: adv.Console(),
		Site: siteConsole, Founding: billing.NewFounding(adminPool),
		// The package's tests all sign in from one address: room for all
		// of them (the per-IP limit itself is tested in app).
		Limits:  &app.Limits{LoginPerIP: ratelimit.Limit{Burst: 50, Period: time.Hour}, LoginFailPerKey: ratelimit.Limit{Burst: 5, Period: 30 * time.Minute}},
		Hashing: config.Identity{Argon2MemoryKiB: 8 * 1024, Argon2Time: 1, Argon2Threads: 1, HashConcurrency: 2}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := mod.Mount(mux, problem.NewResponder(log)); err != nil {
		t.Fatal(err)
	}
	return fixture{h: mux, mod: mod, ten: ten, appPool: appPool, adminPool: adminPool, ids: ids}
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
	items, _ := r.body["items"].([]any)
	if r.status != http.StatusOK || len(items) < 7 {
		t.Fatalf("plans = %d %v", r.status, r.body)
	}
	for _, it := range items {
		if v := it.(map[string]any); v["code"] == code && v["subscribers"] != float64(0) {
			t.Fatalf("a new plan's subscribers = %v", v["subscribers"])
		}
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

func TestConsoleHealthAndStoreBilling(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	storeID := f.store(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/health", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	r := f.do(t, http.MethodGet, adminHost, "/admin/v1/health", "", withCookie(sess))
	if r.status != http.StatusOK || r.body["database"] != true || r.body["redis"] != true || len(r.body["errors"].([]any)) != 0 ||
		r.body["stores"] == nil || r.body["billing"] == nil || r.body["jobs"] == nil {
		t.Fatalf("health = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/metrics", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous metrics = %d", r.status)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/metrics", "", withCookie(sess))
	if ns, _ := r.body["new_stores"].(map[string]any); r.status != http.StatusOK || len(r.body["errors"].([]any)) != 0 || r.body["revenue"] == nil ||
		ns == nil || ns["last_30d"].(float64) < 1 || len(ns["daily"].([]any)) != 30 {
		t.Fatalf("metrics = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/stores/"+storeID+"/billing", "", withCookie(sess))
	if r.status != http.StatusOK || r.body["status"] != "active" || r.body["plan_name_en"] == "" {
		t.Fatalf("billing = %d %v", r.status, r.body)
	}
	settle := "/admin/v1/stores/" + storeID + "/invoices/" + f.ids.New().String() + "/settle"
	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, settle, `{"reference":"TRX-1","reason":"transfer received"}`, http.StatusNotFound},
		{http.MethodPost, settle, `{"reference":"","reason":"transfer received"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, settle, ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/stores/" + storeID + "/invoices/junk/settle", `{"reference":"TRX-1","reason":"transfer"}`, http.StatusNotFound},
		{http.MethodGet, "/admin/v1/stores/not-an-id/billing", "", http.StatusNotFound},
		{http.MethodPost, "/admin/v1/stores/not-an-id/invoices/" + f.ids.New().String() + "/settle", `{"reference":"T","reason":"transfer"}`, http.StatusNotFound},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
	if r = f.do(t, http.MethodPost, adminHost, settle, `{"reference":"TRX-1","reason":"transfer received"}`, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
}

func TestConsoleAgencies(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	storeID := f.store(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/agencies", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	create := `{"name":"وكالة","currency":"EGP","plan_code":"agency","reason":"new agency"}`
	if r := f.do(t, http.MethodPost, adminHost, "/admin/v1/agencies", create, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r := f.do(t, http.MethodPost, adminHost, "/admin/v1/agencies", create, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated {
		t.Fatalf("create = %d %v", r.status, r.body)
	}
	id := r.body["agency"].(map[string]any)["id"].(string)
	base := "/admin/v1/agencies/" + id
	r = f.do(t, http.MethodPost, adminHost, base+"/stores/"+storeID, `{"reason":"joins"}`, withCookie(sess), sameOrigin)
	if ids, _ := r.body["store_ids"].([]any); r.status != http.StatusOK || len(ids) != 1 {
		t.Fatalf("add = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodGet, adminHost, base, "", withCookie(sess))
	if r.status != http.StatusOK || r.body["agency"].(map[string]any)["stores"] != float64(1) {
		t.Fatalf("get = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/agencies", "", withCookie(sess)); r.status != http.StatusOK || len(r.body["items"].([]any)) == 0 {
		t.Fatalf("list = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodPost, adminHost, base+"/stores/"+storeID+"/remove", `{"reason":"leaves"}`, withCookie(sess), sameOrigin)
	if ids, _ := r.body["store_ids"].([]any); r.status != http.StatusOK || len(ids) != 0 {
		t.Fatalf("remove = %d %v", r.status, r.body)
	}
	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/admin/v1/agencies", `{"name":"وكالة","currency":"EGP","plan_code":"pro","reason":"bad plan"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/agencies", ``, http.StatusBadRequest},
		{http.MethodGet, "/admin/v1/agencies/junk", "", http.StatusNotFound},
		{http.MethodGet, "/admin/v1/agencies/" + f.ids.New().String(), "", http.StatusNotFound},
		{http.MethodPost, base + "/stores/" + storeID + "/remove", `{"reason":"again"}`, http.StatusNotFound},
		{http.MethodPost, base + "/stores/not-an-id", `{"reason":"joins"}`, http.StatusNotFound},
		{http.MethodPost, "/admin/v1/agencies/junk/stores/" + storeID, `{"reason":"joins"}`, http.StatusNotFound},
		{http.MethodPost, base + "/stores/" + storeID, ``, http.StatusBadRequest},
		{http.MethodPost, base + "/stores/" + storeID + "/remove", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/agencies/junk/stores/" + storeID + "/remove", `{"reason":"x y"}`, http.StatusNotFound},
		{http.MethodPost, base + "/stores/not-an-id/remove", `{"reason":"x y"}`, http.StatusNotFound},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
}

// TestConsoleAds: AdSense's settings and the banners, from the console
// (ADR-0062). The only test of the package on the ads' tables.
func TestConsoleAds(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/ads", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	save := `{"adsense_enabled":true,"publisher_id":"ca-pub-1234567890123456","units":{"home_top":"1234567890","grid":""},"reason":"link AdSense"}`
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/ads/settings", save, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r := f.do(t, http.MethodPut, adminHost, "/admin/v1/ads/settings", save, withCookie(sess), sameOrigin)
	if r.status != http.StatusOK || r.body["ads_txt"] != "google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0\n" {
		t.Fatalf("save = %d %v", r.status, r.body)
	}
	if u := r.body["units"].(map[string]any); u["home_top"] != "1234567890" || u["grid"] != nil {
		t.Fatalf("units = %v", u)
	}

	// A banner, as the ads module saves one (the bucket is not wired here).
	id := f.ids.New()
	if _, err := f.adminPool.Exec(t.Context(), `INSERT INTO ad_banners (id, title, image_key, width, height, link_url, placement, country, starts_at, weight)
		VALUES ($1, 'افتح متجرك', 'platform/ads/'||$2||'.png', 640, 200, 'https://iftahonline.com/start', 'home_top', 'EG', now(), 10)`, id, id.String()); err != nil {
		t.Fatal(err)
	}
	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/ads", "", withCookie(sess))
	banners, _ := r.body["banners"].([]any)
	if r.status != http.StatusOK || len(banners) != 1 || r.body["settings"].(map[string]any)["adsense_enabled"] != true {
		t.Fatalf("get = %d %v", r.status, r.body)
	}
	if b := banners[0].(map[string]any); b["id"] != id.String() || b["country"] != "EG" || b["active"] != true || b["clicks"] != float64(0) || b["ends_at"] != nil {
		t.Fatalf("banner = %v", b)
	}
	active := "/admin/v1/ads/banners/" + id.String() + "/active"
	if r = f.do(t, http.MethodPut, adminHost, active, `{"active":false,"reason":"campaign over"}`, withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("stop = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/ads", "", withCookie(sess)); r.body["banners"].([]any)[0].(map[string]any)["active"] != false {
		t.Fatalf("after stop = %v", r.body)
	}

	banner := `{"title":"عرض","upload_key":"platform/ads/uploads/x","link_url":"https://iftahonline.com","placement":"grid","starts_at":"2026-11-01T00:00:00Z","weight":5,"reason":"November"}`
	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/admin/v1/ads/settings", `{"adsense_enabled":true,"publisher_id":"","units":{},"reason":"no publisher"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/ads/settings", `{"adsense_enabled":false,"publisher_id":"","units":{},"reason":"x"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/ads/settings", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/ads/uploads", `{"content_type":"image/png"}`, http.StatusServiceUnavailable},
		{http.MethodPost, "/admin/v1/ads/uploads", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/ads/banners", banner, http.StatusServiceUnavailable},
		{http.MethodPost, "/admin/v1/ads/banners", ``, http.StatusBadRequest},
		{http.MethodPut, "/admin/v1/ads/banners/junk/active", `{"active":true,"reason":"again"}`, http.StatusNotFound},
		{http.MethodPut, "/admin/v1/ads/banners/" + f.ids.New().String() + "/active", `{"active":true,"reason":"again"}`, http.StatusNotFound},
		{http.MethodPut, active, ``, http.StatusBadRequest},
		{http.MethodPut, active, `{"active":true,"reason":"x"}`, http.StatusUnprocessableEntity},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
}

// TestConsoleDuplicatePayments: the list, and a refund's refusals (ADR-0063).
func TestConsoleDuplicatePayments(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)
	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/billing/duplicates", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/billing/duplicates", "", withCookie(sess)); r.status != http.StatusOK || r.body["items"] == nil {
		t.Fatalf("list = %d %v", r.status, r.body)
	}
	refund := "/admin/v1/billing/duplicates/" + f.ids.New().String() + "/refunded"
	if r := f.do(t, http.MethodPost, adminHost, refund, `{"reference":"PMB-1","reason":"refunded on Paymob"}`, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	for _, bad := range []struct {
		path, body string
		status     int
	}{
		{refund, `{"reference":"PMB-1","reason":"refunded on Paymob"}`, http.StatusNotFound},
		{refund, `{"reference":"PMB-1","reason":"x"}`, http.StatusUnprocessableEntity},
		{refund, ``, http.StatusBadRequest},
		{"/admin/v1/billing/duplicates/junk/refunded", `{"reference":"PMB-1","reason":"refunded"}`, http.StatusNotFound},
	} {
		if got := f.do(t, http.MethodPost, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("POST %s = %d %v; want %d", bad.path, got.status, got.body, bad.status)
		}
	}
}

// TestConsoleSite: the platform site's contact details and FAQ, from the
// console (ADR-0073). The only test of the package on the site's tables.
func TestConsoleSite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/site", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	r := f.do(t, http.MethodGet, adminHost, "/admin/v1/site", "", withCookie(sess))
	if faqs, _ := r.body["faqs"].([]any); r.status != http.StatusOK || len(faqs) != 10 || r.body["settings"].(map[string]any)["whatsapp"] != "+201153450716" {
		t.Fatalf("get = %d %v", r.status, r.body)
	}

	save := `{"whatsapp":"+201153450716","whatsapp_intl":"+447700900123","email":"hello@iftahonline.com","company_number":"12345678",
		"socials":{"instagram":"https://instagram.com/iftahonline","x":""},"reason":"contact details"}`
	if r = f.do(t, http.MethodPut, adminHost, "/admin/v1/site/settings", save, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r = f.do(t, http.MethodPut, adminHost, "/admin/v1/site/settings", save, withCookie(sess), sameOrigin)
	if so := r.body["socials"].(map[string]any); r.status != http.StatusOK || so["instagram"] != "https://instagram.com/iftahonline" || so["x"] != nil ||
		r.body["whatsapp_intl"] != "+447700900123" {
		t.Fatalf("save = %d %v", r.status, r.body)
	}

	add := `{"position":15,"on_home":false,"question_ar":"هل تدعمون فودافون كاش؟","answer_ar":"قريبًا.","reason":"a common question"}`
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/site/faqs", add, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["question_en"] != "" {
		t.Fatalf("add = %d %v", r.status, r.body)
	}
	one := "/admin/v1/site/faqs/" + r.body["id"].(string)
	edit := `{"position":15,"on_home":true,"question_ar":"هل تدعمون فودافون كاش؟","answer_ar":"قريبًا.","question_en":"Vodafone Cash?","answer_en":"Soon.","reason":"English too"}`
	if r = f.do(t, http.MethodPut, adminHost, one, edit, withCookie(sess), sameOrigin); r.status != http.StatusOK || r.body["on_home"] != true || r.body["answer_en"] != "Soon." {
		t.Fatalf("edit = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodDelete, adminHost, one+"?reason=answered+elsewhere", "", withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site delete = %d", r.status)
	}
	if r = f.do(t, http.MethodDelete, adminHost, one+"?reason=answered+elsewhere", "", withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d %v", r.status, r.body)
	}

	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/admin/v1/site/settings", `{"whatsapp":"0115","whatsapp_intl":"","email":"","company_number":"","socials":{},"reason":"bad number"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/site/settings", `{"whatsapp":"","whatsapp_intl":"","email":"","company_number":"","socials":{"facebook":"https://evil.example"},"reason":"bad link"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/site/settings", `{"whatsapp":"","whatsapp_intl":"","email":"","company_number":"","socials":{},"reason":"x"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/site/settings", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/site/faqs", `{"position":0,"on_home":false,"question_ar":"؟","answer_ar":"لا","reason":"too short"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/faqs", `{"position":0,"on_home":false,"question_ar":"سؤال كامل؟","answer_ar":"إجابة","question_en":"Only a question?","reason":"half English"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/faqs", ``, http.StatusBadRequest},
		{http.MethodPut, one, edit, http.StatusNotFound},
		{http.MethodPut, "/admin/v1/site/faqs/junk", edit, http.StatusNotFound},
		{http.MethodPut, one, ``, http.StatusBadRequest},
		{http.MethodDelete, one + "?reason=again+please", "", http.StatusNotFound},
		{http.MethodDelete, "/admin/v1/site/faqs/junk?reason=again+please", "", http.StatusNotFound},
		{http.MethodDelete, one, "", http.StatusBadRequest},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/site", "", withCookie(sess)); len(r.body["faqs"].([]any)) != 10 {
		t.Fatalf("after all = %v", r.body)
	}
}

// TestConsoleSiteAbout: the founding story, the people and the reviews
// from the console (S4b-1). Images need the bucket, not wired here.
func TestConsoleSiteAbout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)

	story := `{"story_ar":"بدأنا من متجر صغير.","story_en":"","video_url":"https://youtu.be/dQw4w9WgXcQ","reason":"the story"}`
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/site/story", story, sameOrigin); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/site/story", story, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/site/story", story, withCookie(sess), sameOrigin); r.status != http.StatusOK || r.body["video_id"] != "dQw4w9WgXcQ" {
		t.Fatalf("story = %d %v", r.status, r.body)
	}

	person := `{"kind":"founder","position":0,"name_ar":"أحمد","role_ar":"المدير التقني","linkedin_url":"https://www.linkedin.com/in/ahmed","reason":"a founder"}`
	r := f.do(t, http.MethodPost, adminHost, "/admin/v1/site/people", person, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["photo_url"] != "" || r.body["kind"] != "founder" {
		t.Fatalf("add person = %d %v", r.status, r.body)
	}
	one := "/admin/v1/site/people/" + r.body["id"].(string)
	edit := `{"kind":"founder","position":0,"name_ar":"أحمد","name_en":"Ahmed","role_ar":"المدير التقني","role_en":"CTO","reason":"English"}`
	if r = f.do(t, http.MethodPut, adminHost, one, edit, withCookie(sess), sameOrigin); r.status != http.StatusOK || r.body["role_en"] != "CTO" || r.body["linkedin_url"] != "" {
		t.Fatalf("edit person = %d %v", r.status, r.body)
	}

	review := `{"position":0,"name":"منى","store_name":"متجر منى","quote_ar":"قلّت الطلبات الوهمية عندي.","reason":"a real review"}`
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/site/testimonials", review, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["quote_en"] != "" {
		t.Fatalf("add review = %d %v", r.status, r.body)
	}
	oneReview := "/admin/v1/site/testimonials/" + r.body["id"].(string)

	r = f.do(t, http.MethodGet, adminHost, "/admin/v1/site", "", withCookie(sess))
	if people, _ := r.body["people"].([]any); r.status != http.StatusOK || len(people) != 1 || len(r.body["testimonials"].([]any)) != 1 ||
		r.body["story"].(map[string]any)["story_ar"] != "بدأنا من متجر صغير." || r.body["images"].(map[string]any)["logo_url"] != "" {
		t.Fatalf("get = %d %v", r.status, r.body)
	}

	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/admin/v1/site/story", `{"story_ar":"","story_en":"","video_url":"https://vimeo.com/1","reason":"a video"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/site/story", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/site/uploads", `{"content_type":"image/png"}`, http.StatusServiceUnavailable},
		{http.MethodPost, "/admin/v1/site/uploads", ``, http.StatusBadRequest},
		{http.MethodPut, "/admin/v1/site/images/logo", `{"upload_key":"platform/site/uploads/x","reason":"the logo"}`, http.StatusServiceUnavailable},
		{http.MethodPut, "/admin/v1/site/images/favicon", `{"upload_key":"platform/site/uploads/x","reason":"the icon"}`, http.StatusNotFound},
		{http.MethodDelete, "/admin/v1/site/images/logo", "", http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/site/people", `{"kind":"investor","position":0,"name_ar":"أحمد","role_ar":"شريك","reason":"no"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/people", `{"kind":"team","position":0,"name_ar":"أحمد","role_ar":"دعم","x_url":"https://evil.example","reason":"bad link"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/people", ``, http.StatusBadRequest},
		{http.MethodPut, "/admin/v1/site/people/junk", edit, http.StatusNotFound},
		{http.MethodPut, "/admin/v1/site/people/" + f.ids.New().String(), edit, http.StatusNotFound},
		{http.MethodPost, "/admin/v1/site/testimonials", `{"position":0,"name":"منى","quote_ar":"ممتاز","reason":"too short"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/admin/v1/site/testimonials/junk", review, http.StatusNotFound},
		{http.MethodDelete, oneReview, "", http.StatusBadRequest},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}

	if r = f.do(t, http.MethodDelete, adminHost, one+"?reason=left+the+team", "", withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("delete person = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodDelete, adminHost, one+"?reason=left+the+team", "", withCookie(sess), sameOrigin); r.status != http.StatusNotFound {
		t.Fatalf("delete twice = %d", r.status)
	}
	if r = f.do(t, http.MethodDelete, adminHost, oneReview+"?reason=withdrawn", "", withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("delete review = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodDelete, adminHost, "/admin/v1/site/images/logo?reason=no+logo", "", withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("remove logo = %d %v", r.status, r.body)
	}
}

// TestConsoleFounding: the founding-merchants program from the console:
// its settings with a reason, and its members (none yet).
func TestConsoleFounding(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/founding", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	save := `{"enabled":true,"seats":100,"discount_bps":2000,"reason":"launch the program"}`
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/founding", save, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	if r := f.do(t, http.MethodPut, adminHost, "/admin/v1/founding", save, withCookie(sess), sameOrigin); r.status != http.StatusOK || r.body["enabled"] != true {
		t.Fatalf("save = %d %v", r.status, r.body)
	}
	r := f.do(t, http.MethodGet, adminHost, "/admin/v1/founding", "", withCookie(sess))
	if p := r.body["program"].(map[string]any); r.status != http.StatusOK || p["seats"] != float64(100) || p["discount_bps"] != float64(2000) ||
		len(r.body["members"].([]any)) != 0 {
		t.Fatalf("get = %d %v", r.status, r.body)
	}
	for _, bad := range []struct {
		body   string
		status int
	}{
		{`{"enabled":true,"seats":0,"discount_bps":2000,"reason":"no seats"}`, http.StatusUnprocessableEntity},
		{`{"enabled":true,"seats":100,"discount_bps":9500,"reason":"too much"}`, http.StatusUnprocessableEntity},
		{`{"enabled":true,"seats":100,"discount_bps":2000,"reason":"x"}`, http.StatusUnprocessableEntity},
		{``, http.StatusBadRequest},
	} {
		if got := f.do(t, http.MethodPut, adminHost, "/admin/v1/founding", bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("PUT %s = %d %v; want %d", bad.body, got.status, got.body, bad.status)
		}
	}
	if r = f.do(t, http.MethodPut, adminHost, "/admin/v1/founding", `{"enabled":false,"seats":100,"discount_bps":2000,"reason":"off again"}`,
		withCookie(sess), sameOrigin); r.status != http.StatusOK {
		t.Fatalf("off = %d", r.status)
	}
}

// TestConsoleSiteGuides: the guides from the console (S4c).
func TestConsoleSiteGuides(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess := f.login(t)

	if r := f.do(t, http.MethodGet, adminHost, "/admin/v1/site/guides", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	body := `{"slug":"reduce-fake-orders","status":"draft","title_ar":"كيف تقلل الطلبات الوهمية","summary_ar":"خطوات عملية لتقليل رفض الاستلام.",` +
		`"body_ar":"# أولًا\n\nاتصل بالعميل قبل الشحن، وأكد العنوان والمبلغ.","reason":"the first guide"}`
	if r := f.do(t, http.MethodPost, adminHost, "/admin/v1/site/guides", body, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	r := f.do(t, http.MethodPost, adminHost, "/admin/v1/site/guides", body, withCookie(sess), sameOrigin)
	if r.status != http.StatusCreated || r.body["status"] != "draft" || r.body["published_at"] != nil {
		t.Fatalf("create = %d %v", r.status, r.body)
	}
	one := "/admin/v1/site/guides/" + r.body["id"].(string)
	publish := strings.Replace(body, `"status":"draft"`, `"status":"published"`, 1)
	if r = f.do(t, http.MethodPut, adminHost, one, publish, withCookie(sess), sameOrigin); r.status != http.StatusOK || r.body["published_at"] == nil {
		t.Fatalf("publish = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, one, "", withCookie(sess)); r.status != http.StatusOK || !strings.HasPrefix(r.body["body_ar"].(string), "# أولًا") {
		t.Fatalf("get = %d %v", r.status, r.body)
	}
	// The five seeded drafts (00086) and this one.
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/site/guides", "", withCookie(sess)); len(r.body["items"].([]any)) != 6 {
		t.Fatalf("list = %v", r.body)
	}
	r = f.do(t, http.MethodPost, adminHost, "/admin/v1/site/guides/preview", `{"markdown":"# عنوان\n\n<script>x</script>"}`, withCookie(sess), sameOrigin)
	if html, _ := r.body["html"].(string); r.status != http.StatusOK || !strings.Contains(html, "<h2") || strings.Contains(html, "<script") {
		t.Fatalf("preview = %d %v", r.status, r.body)
	}

	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/admin/v1/site/guides", body, http.StatusConflict},
		{http.MethodPost, "/admin/v1/site/guides", strings.Replace(body, "reduce-fake-orders", "Fake Orders", 1), http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/guides", strings.Replace(body, `"title_ar":"كيف تقلل الطلبات الوهمية",`, `"title_ar":"كيف تقلل الطلبات","title_en":"Only a title",`, 1), http.StatusUnprocessableEntity},
		{http.MethodPost, "/admin/v1/site/guides", ``, http.StatusBadRequest},
		{http.MethodPost, "/admin/v1/site/guides/preview", ``, http.StatusBadRequest},
		{http.MethodGet, "/admin/v1/site/guides/junk", "", http.StatusNotFound},
		{http.MethodGet, "/admin/v1/site/guides/" + f.ids.New().String(), "", http.StatusNotFound},
		{http.MethodPut, "/admin/v1/site/guides/" + f.ids.New().String(), publish, http.StatusNotFound},
		{http.MethodDelete, one, "", http.StatusBadRequest},
	} {
		if got := f.do(t, bad.method, adminHost, bad.path, bad.body, withCookie(sess), sameOrigin); got.status != bad.status {
			t.Errorf("%s %s = %d %v; want %d", bad.method, bad.path, got.status, got.body, bad.status)
		}
	}
	if r = f.do(t, http.MethodDelete, adminHost, one+"?reason=rewritten", "", withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d", r.status)
	}
}
