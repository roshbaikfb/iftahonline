package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type fixture struct {
	svc     *app.Service
	app     *pgxpool.Pool
	owner   *pgxpool.Pool
	ids     *ulid.Generator
	qc      *testkit.QueryCounter
	ten     *tenancy.Module
	rdb     *redis.Client
	keyring *secretbox.Keyring
	ins     *river.Client[pgx.Tx]
	log     *slog.Logger
}

// testLimits are tight so throttling is testable; each test uses fresh
// emails and IPs, so tests do not share buckets.
func testLimits() app.Limits {
	return app.Limits{
		SignupPerIP:      ratelimit.Limit{Burst: 3, Period: time.Hour},
		LoginPerIP:       ratelimit.Limit{Burst: 20, Period: time.Hour},
		LoginFailPerUser: ratelimit.Limit{Burst: 3, Period: time.Hour},
		OTPPerPhone:      ratelimit.Limit{Burst: 3, Period: time.Hour},
		OTPPerPhoneDay:   ratelimit.Limit{Burst: 10, Period: 24 * time.Hour},
		OTPPerIP:         ratelimit.Limit{Burst: 10, Period: time.Hour},
		MFAFailPerUser:   ratelimit.Limit{Burst: 10, Period: time.Hour},
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, qc := testkit.CountingPool(t, infra.AppURL)
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
	ten, err := tenancy.New(tenancy.Deps{Pool: pool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, Audit: app.NewRecorder(ids),
		Config: config.Tenancy{BaseDomain: "iftahonline.com", HostCacheSize: 10, HostCacheTTL: time.Minute,
			NegativeCacheSize: 10, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.New(password.Params{MemoryKiB: 8 * 1024, Time: 1, Threads: 1}, 4)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := ratelimit.New(rdb, "identity-test")
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=")
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{
		DB: pool, Hasher: hasher, Limiter: limiter, Limits: testLimits(), Stores: ten.Service(), IDs: ids, Log: log,
		Redis: rdb, Keyring: keyring, Pepper: []byte("dev-only-otp-pepper-not-a-secret"), Jobs: ins, OTPEnabled: true,
	})
	return fixture{svc: svc, app: pool, owner: owner, ids: ids, qc: qc, ten: ten, rdb: rdb, keyring: keyring, ins: ins, log: log}
}

func (f fixture) unique(prefix string) string {
	return prefix + strings.ToLower(f.ids.New().String()[16:])
}

// device returns a request context from a fresh IP, so per-IP limits do
// not leak between tests.
func (f fixture) device(t *testing.T) context.Context {
	t.Helper()
	b := f.ids.New()
	ip := netip.AddrFrom4([4]byte{10, b[13], b[14], b[15]})
	return app.WithRequestMeta(t.Context(), app.RequestMeta{UserAgent: "test-agent/1.0", IP: ip})
}

const goodPassword = "correct horse battery"

func (f fixture) signup(t *testing.T) (app.AuthResult, string) {
	t.Helper()
	email := f.unique("user") + "@example.com"
	res, err := f.svc.Signup(f.device(t), app.SignupInput{
		Email: email, Password: goodPassword, Name: "Ahmed",
		Store: tenancy.NewStore{Slug: f.unique("shop-"), Name: "متجر", Currency: "EGP", Locale: "ar", Timezone: "Africa/Cairo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, email
}

func fieldCodes(err error) map[string]string {
	out := map[string]string{}
	if e, ok := errors.AsType[*errs.Error](err); ok {
		for _, f := range e.Fields {
			out[f.Field] = f.Code
		}
	}
	return out
}

func TestSignup(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, email := f.signup(t)

	if res.User.Email != email || res.User.Status != domain.UserActive || res.StoreID.IsZero() || len(res.Token) != 43 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.HasPrefix(res.User.PasswordHash, "$argon2id$") {
		t.Fatal("password must be stored as argon2id")
	}
	// The owner can act in the new store with the returned token.
	ctx := tenant.WithStore(t.Context(), res.StoreID)
	p, err := f.svc.AuthenticateInStore(ctx, string(res.Token))
	if err != nil || p.Role != authz.RoleOwner || p.UserID != res.User.ID || p.StoreID != res.StoreID {
		t.Fatalf("principal = %+v, %v", p, err)
	}
	// The store exists with its subdomain, and creation was audited.
	st, err := f.ten.Service().GetStore(ctx)
	if err != nil || st.ID != res.StoreID {
		t.Fatalf("store = %+v, %v", st, err)
	}
	pctx := authz.WithPrincipal(ctx, p)
	page, err := f.svc.ListAudit(pctx, "", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != "store.created" ||
		page.Items[0].ActorUserID == nil || *page.Items[0].ActorUserID != res.User.ID || page.Items[0].IPPrefix == "" {
		t.Fatalf("audit = %+v, %v", page, err)
	}
}

func TestSignupValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.device(t)
	good := tenancy.NewStore{Slug: f.unique("v-"), Name: "x", Currency: "EGP", Locale: "ar", Timezone: "UTC"}

	_, err := f.svc.Signup(ctx, app.SignupInput{Email: "bad", Password: "short", Name: "", Store: good})
	if c := fieldCodes(err); c["email"] != "invalid" || c["password"] != "too_short" || c["name"] != "invalid" {
		t.Fatalf("user fields = %v (%v)", c, err)
	}

	// Store errors are namespaced, and nothing is left behind.
	email := f.unique("v") + "@example.com"
	_, err = f.svc.Signup(ctx, app.SignupInput{Email: email, Password: goodPassword, Name: "A",
		Store: tenancy.NewStore{Slug: "admin", Name: "", Currency: "USD", Locale: "ar", Timezone: "UTC"}})
	if c := fieldCodes(err); c["store.slug"] != "reserved" || c["store.name"] != "length" || c["store.currency"] != "unsupported" {
		t.Fatalf("store fields = %v (%v)", c, err)
	}
	var n int
	if err = f.owner.QueryRow(t.Context(), "SELECT count(*) FROM users WHERE email = $1", email).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed sign-up left a user: %d, %v", n, err)
	}

	// A taken email is reported on the email field, case-insensitively.
	_, taken := f.signup(t)
	_, err = f.svc.Signup(f.device(t), app.SignupInput{Email: strings.ToUpper(taken), Password: goodPassword, Name: "B",
		Store: tenancy.NewStore{Slug: f.unique("t-"), Name: "x", Currency: "EGP", Locale: "ar", Timezone: "UTC"}})
	if c := fieldCodes(err); c["email"] != "taken" {
		t.Fatalf("taken email = %v (%v)", c, err)
	}
}

func TestSignupIsRateLimitedPerIP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.device(t)
	for i := range 4 {
		_, err := f.svc.Signup(ctx, app.SignupInput{Email: "x", Password: "y", Name: "z"})
		if i < 3 && errs.Is(err, errs.KindRateLimited) {
			t.Fatalf("attempt %d limited too early", i)
		}
		if i == 3 && errs.Is(err, errs.KindValidation) {
			// Validation runs first, so bad input never consumes the budget.
			return
		}
	}
	// Valid-looking attempts consume it.
	for i := range 4 {
		_, err := f.svc.Signup(ctx, app.SignupInput{Email: f.unique("rl") + "@example.com", Password: goodPassword, Name: "A",
			Store: tenancy.NewStore{Slug: "admin", Name: "x", Currency: "EGP", Locale: "ar", Timezone: "UTC"}})
		if i == 3 && !errs.Is(err, errs.KindRateLimited) {
			t.Fatalf("4th sign-up from one IP: %v", err)
		}
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, email := f.signup(t)

	got, err := f.svc.Login(f.device(t), " "+strings.ToUpper(email)+" ", goodPassword)
	if err != nil || got.User.ID != res.User.ID || got.Token == res.Token || !got.StoreID.IsZero() {
		t.Fatalf("login = %+v, %v", got, err)
	}
	id, err := f.svc.Authenticate(t.Context(), string(got.Token))
	if err != nil || id.UserID != res.User.ID || id.SessionID != got.Session.ID {
		t.Fatalf("authenticate = %+v, %v", id, err)
	}

	// Wrong password, unknown email and junk all look the same.
	for name, try := range map[string][2]string{
		"wrong password": {email, "not the password"},
		"unknown email":  {f.unique("nobody") + "@example.com", goodPassword},
		"bad email":      {"not-an-email", goodPassword},
		"huge password":  {email, strings.Repeat("x", 600)},
	} {
		if _, err := f.svc.Login(f.device(t), try[0], try[1]); !errs.Is(err, errs.KindUnauthenticated) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

// TestLoginLockout: after the failure budget for one email is spent, even
// the right password is refused until it refills; success clears it.
func TestLoginLockout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, email := f.signup(t)

	if _, err := f.svc.Login(f.device(t), email, "wrong one"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(f.device(t), email, goodPassword); err != nil {
		t.Fatalf("success after one failure: %v", err)
	}
	for range 3 { // budget is 3, and the success above reset it
		if _, err := f.svc.Login(f.device(t), email, "wrong again"); !errs.Is(err, errs.KindUnauthenticated) {
			t.Fatal(err)
		}
	}
	_, err := f.svc.Login(f.device(t), email, goodPassword)
	e, ok := errors.AsType[*errs.Error](err)
	if !ok || e.Kind != errs.KindRateLimited || e.RetryAfter <= 0 {
		t.Fatalf("locked account: %v", err)
	}
	// Unknown emails are locked the same way (no enumeration by lockout).
	ghost := f.unique("ghost") + "@example.com"
	for range 3 {
		_, _ = f.svc.Login(f.device(t), ghost, "x")
	}
	if _, err := f.svc.Login(f.device(t), ghost, "x"); !errs.Is(err, errs.KindRateLimited) {
		t.Fatalf("unknown email lockout: %v", err)
	}
}

func TestLoginPerIPLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.device(t)
	var last error
	for range 21 {
		_, last = f.svc.Login(ctx, f.unique("ip")+"@example.com", "x")
	}
	if !errs.Is(last, errs.KindRateLimited) {
		t.Fatalf("21st attempt from one IP: %v", last)
	}
}

func TestDisabledUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, email := f.signup(t)
	if _, err := f.owner.Exec(t.Context(), "UPDATE users SET status = 'disabled' WHERE id = $1", res.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(f.device(t), email, goodPassword); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disabled login: %v", err)
	}
	// Existing sessions stop working at once.
	if _, err := f.svc.Authenticate(t.Context(), string(res.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disabled session: %v", err)
	}
	ctx := tenant.WithStore(t.Context(), res.StoreID)
	if _, err := f.svc.AuthenticateInStore(ctx, string(res.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disabled session in store: %v", err)
	}
}

func TestPasswordRehashOnLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, email := f.signup(t)
	// Store a hash with older (weaker) parameters.
	weak, err := password.New(password.Params{MemoryKiB: 8 * 1024, Time: 2, Threads: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := weak.Hash(t.Context(), goodPassword)
	if _, err := f.owner.Exec(t.Context(), "UPDATE users SET password_hash = $1 WHERE id = $2", h, res.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(f.device(t), email, goodPassword); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := f.owner.QueryRow(t.Context(), "SELECT password_hash FROM users WHERE id = $1", res.User.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "m=8192,t=1,p=1") {
		t.Fatalf("hash not upgraded: %s", stored)
	}
}

// TestAuthenticateInStore covers the membership gate and its query budget.
func TestAuthenticateInStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, _ := f.signup(t)
	b, _ := f.signup(t)

	inA := tenant.WithStore(t.Context(), a.StoreID)
	inB := tenant.WithStore(t.Context(), b.StoreID)

	f.qc.Reset()
	if _, err := f.svc.AuthenticateInStore(inA, string(a.Token)); err != nil {
		t.Fatal(err)
	}
	// BEGIN, set tenant, one session+membership lookup, COMMIT: constant,
	// whatever the store or user.
	if got := f.qc.Count(); got != 4 {
		t.Fatalf("statements for an authenticated store request = %d, want 4", got)
	}

	tests := []struct {
		name  string
		ctx   context.Context
		token string
		kind  errs.Kind
	}{
		{"A in B's store is not found", inB, string(a.Token), errs.KindNotFound},
		{"B in A's store is not found", inA, string(b.Token), errs.KindNotFound},
		{"unknown store is not found", tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()}), string(a.Token), errs.KindNotFound},
		{"junk token", inA, "junk", errs.KindUnauthenticated},
		{"well-formed unknown token", inA, strings.Repeat("A", 43), errs.KindUnauthenticated},
		{"no tenant", t.Context(), string(a.Token), errs.KindNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.AuthenticateInStore(tt.ctx, tt.token); !errs.Is(err, tt.kind) {
				t.Fatalf("err = %v, want %v", err, tt.kind)
			}
		})
	}
}

func TestMeSessionsLogout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, email := f.signup(t)
	b, _ := f.signup(t)
	second, err := f.svc.Login(f.device(t), email, goodPassword)
	if err != nil {
		t.Fatal(err)
	}

	asA := app.WithIdentity(t.Context(), app.Identity{UserID: a.User.ID, SessionID: a.Session.ID})
	u, ms, err := f.svc.Me(asA)
	if err != nil || u.ID != a.User.ID || len(ms) != 1 || ms[0].StoreID != a.StoreID || ms[0].Role != authz.RoleOwner {
		t.Fatalf("me = %+v %+v %v", u, ms, err)
	}

	ss, current, err := f.svc.Sessions(asA)
	if err != nil || len(ss) != 2 || current != a.Session.ID || ss[0].UserAgent != "test-agent/1.0" || !strings.HasSuffix(ss[0].IPPrefix, "/24") {
		t.Fatalf("sessions = %+v, %v", ss, err)
	}

	// B cannot revoke A's session; A can.
	asB := app.WithIdentity(t.Context(), app.Identity{UserID: b.User.ID, SessionID: b.Session.ID})
	if err := f.svc.RevokeSession(asB, second.Session.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("B revokes A's session: %v", err)
	}
	if _, err := f.svc.Authenticate(t.Context(), string(second.Token)); err != nil {
		t.Fatalf("A's session must survive B's attempt: %v", err)
	}
	if err := f.svc.RevokeSession(asA, second.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(t.Context(), string(second.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("revoked session: %v", err)
	}
	if err := f.svc.RevokeSession(asA, second.Session.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("double revoke: %v", err)
	}

	if err := f.svc.Logout(asA); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(t.Context(), string(a.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("after logout: %v", err)
	}

	// Without an identity everything is 401.
	if _, _, err := f.svc.Me(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Sessions(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
	if err := f.svc.RevokeSession(t.Context(), a.Session.ID); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
	if err := f.svc.Logout(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
	ghost := app.WithIdentity(t.Context(), app.Identity{UserID: f.ids.New(), SessionID: f.ids.New()})
	if _, _, err := f.svc.Me(ghost); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("deleted user: %v", err)
	}
}

func TestSessionTouchSlidesExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, _ := f.signup(t)
	// Pretend the session was last seen an hour ago.
	if _, err := f.owner.Exec(t.Context(), "UPDATE user_sessions SET last_seen_at = now() - interval '1 hour', expires_at = now() + interval '1 hour' WHERE id = $1", a.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(t.Context(), string(a.Token)); err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err := f.owner.QueryRow(t.Context(), "SELECT expires_at FROM user_sessions WHERE id = $1", a.Session.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) < 6*24*time.Hour {
		t.Fatalf("expiry not slid: %v", expires)
	}
	// An expired session is gone.
	if _, err := f.owner.Exec(t.Context(), "UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE id = $1", a.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(t.Context(), string(a.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("expired: %v", err)
	}
}

func TestAuditLog(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, _ := f.signup(t)
	b, _ := f.signup(t)
	ctxA := authz.WithPrincipal(tenant.WithStore(f.device(t), a.StoreID),
		authz.Principal{UserID: a.User.ID, StoreID: a.StoreID, Role: authz.RoleOwner})
	ctxB := tenant.WithStore(t.Context(), b.StoreID)

	// Tenancy actions are audited in their own transactions.
	for i := range 4 {
		if _, err := f.ten.Service().AddCustomDomain(ctxA, f.unique("d")+".example.com"); err != nil {
			t.Fatalf("domain %d: %v", i, err)
		}
	}
	rec := app.NewRecorder(f.ids)
	err := postgres.InTenantTx(ctxA, f.app, func(tx pgx.Tx) error {
		return rec.RecordTx(ctxA, tx, audit.Entry{Action: "Bad Action"})
	})
	if err == nil {
		t.Fatal("invalid action must be rejected")
	}

	// Paging: 5 entries (store.created + 4 domain.added), pages of 2.
	var all []string
	cursor := ""
	for range 5 {
		page, listErr := f.svc.ListAudit(ctxA, cursor, 2)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, e := range page.Items {
			all = append(all, e.Action)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if strings.Join(all, ",") != "domain.added,domain.added,domain.added,domain.added,store.created" {
		t.Fatalf("pages = %v", all)
	}
	if _, err = f.svc.ListAudit(ctxA, "!!", 10); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err = f.svc.ListAudit(ctxA, "", 500); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("huge limit: %v", err)
	}

	// B sees only B's entry.
	pageB, err := f.svc.ListAudit(ctxB, "", 50)
	if err != nil || len(pageB.Items) != 1 || pageB.Items[0].TargetID != b.StoreID.String() {
		t.Fatalf("B's log = %+v, %v", pageB, err)
	}

	// Append-only, for app_user and even for the table owner.
	_, err = f.app.Exec(t.Context(), "DELETE FROM audit_log")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("app_user delete: %v", err)
	}
	err = pgx.BeginFunc(t.Context(), f.owner, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(t.Context(), "SELECT set_config('app.store_id', $1::uuid::text, true)", a.StoreID.ID); execErr != nil {
			return execErr
		}
		_, execErr := tx.Exec(t.Context(), "UPDATE audit_log SET action = 'x.y' WHERE store_id = $1", a.StoreID.ID)
		return execErr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("owner update: %v", err)
	}

	// A raw query with no tenant sees no tenant rows at all.
	testkit.AssertNoRowsWithoutTenant(t, f.app)
}

func TestAuditPartitionsWorker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	w := &app.AuditPartitionsWorker{DB: f.app}
	if err := w.Work(t.Context(), &river.Job[app.AuditPartitionsArgs]{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.owner.QueryRow(t.Context(),
		"SELECT count(*) FROM pg_inherits WHERE inhparent = 'public.audit_log'::regclass").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < app.AuditPartitionsMonthsAhead+1 {
		t.Fatalf("partitions = %d", n)
	}
	// Idempotent, and every partition passes the RLS catalog check.
	if err := w.Work(t.Context(), &river.Job[app.AuditPartitionsArgs]{}); err != nil {
		t.Fatal(err)
	}
	testkit.AssertTenantTablesUseRLS(t, f.owner, "store_memberships.member_self_read")
	if app.AuditPartitionsPeriodic() == nil {
		t.Fatal("periodic job")
	}
}

// TestPartitionCreationDoesNotDeadlockWriters reproduces the CI deadlock:
// creating partitions while sign-ups insert into stores and audit_log.
// Partition creation must never take a lock that conflicts with inserts.
func TestPartitionCreationDoesNotDeadlockWriters(t *testing.T) {
	f := newFixture(t)
	w := &app.AuditPartitionsWorker{DB: f.app}
	stop := make(chan struct{})
	errc := make(chan error, 64)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, err := f.svc.Signup(f.device(t), app.SignupInput{
					Email: f.unique("dl") + "@example.com", Password: goodPassword, Name: "D",
					Store: tenancy.NewStore{Slug: f.unique("dl-"), Name: "D", Currency: "EGP", Locale: "ar", Timezone: "UTC"},
				})
				if err != nil && !errs.Is(err, errs.KindRateLimited) {
					errc <- err
					return
				}
			}
		})
	}
	// Each run adds partitions further ahead: real DDL while writers run.
	for months := 3; months <= 24; months++ {
		if _, err := f.owner.Exec(t.Context(), "SELECT app.ensure_audit_log_partitions($1)", months); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("ensure %d months: %v", months, err)
		}
	}
	if err := w.Work(t.Context(), &river.Job[app.AuditPartitionsArgs]{}); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Errorf("sign-up during partition creation: %v", err)
	}
	testkit.AssertTenantTablesUseRLS(t, f.owner, "store_memberships.member_self_read")
}
