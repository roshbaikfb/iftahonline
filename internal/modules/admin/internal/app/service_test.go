package app_test

import (
	"context"
	"encoding/base32"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type fixture struct {
	svc   *app.Service
	cli   *app.Service // no limiter, directory or stores: what the CLI gets
	owner *pgxpool.Pool
	app   *pgxpool.Pool
	ten   *tenancy.Module
	ids   *ulid.Generator
	imp   *fakeImpersonator
}

const adminPW = "a long admin passphrase"

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
	adminPool, appPool, owner := open(infra.AdminURL), open(infra.AppURL), open(infra.OwnerURL)
	opts, _ := redis.ParseURL(infra.RedisURL)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, err := river.NewClient(riverpgxv5.New(appPool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ten, err := tenancy.New(tenancy.Deps{Pool: appPool, Redis: rdb, Jobs: ins, IDs: ids, Log: log, AdminPool: adminPool,
		Audit: identity.NewAuditRecorder(ids), // as cmd/platform wires it
		Config: config.Tenancy{BaseDomain: "iftahonline.com", HostCacheSize: 10, HostCacheTTL: time.Minute,
			NegativeCacheSize: 10, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	hasher, _ := password.New(password.Params{MemoryKiB: 8 * 1024, Time: 1, Threads: 1}, 4)
	keyring, _ := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=")
	limiter, _ := ratelimit.New(rdb, "admin-test")
	limits := app.Limits{LoginPerIP: ratelimit.Limit{Burst: 50, Period: time.Hour}, LoginFailPerKey: ratelimit.Limit{Burst: 3, Period: time.Hour}}
	d := app.Deps{DB: adminPool, Hasher: hasher, Limiter: limiter, Limits: limits, Keyring: keyring, IDs: ids, Log: log,
		Directory: ten.Directory(), Stores: ten.Service()}
	imp := &fakeImpersonator{}
	d.Impersonate, d.DashboardURL = imp.open, "https://app.iftahonline.com/"
	cli := d
	cli.Limiter, cli.Directory, cli.Stores, cli.Impersonate = nil, nil, nil, nil
	return fixture{svc: app.NewService(d), cli: app.NewService(cli), owner: owner, app: appPool, ten: ten, ids: ids, imp: imp}
}

func (f fixture) device(t *testing.T) context.Context {
	b := f.ids.New()
	return app.WithRequestMeta(t.Context(), app.RequestMeta{UserAgent: "Console/1", IP: netip.AddrFrom4([4]byte{192, 0, 2, b[15]})})
}

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.HOTP(raw, totp.Step(at), 6)
}

func (f fixture) createAdmin(t *testing.T) (app.Created, string) {
	t.Helper()
	email := "ops-" + strings.ToLower(f.ids.New().String()[16:]) + "@iftahonline.com"
	c, err := f.cli.CreateAdmin(t.Context(), " "+strings.ToUpper(email)+" ", "Ops", adminPW)
	if err != nil {
		t.Fatal(err)
	}
	return c, email
}

func (f fixture) createStore(t *testing.T) tenant.StoreID {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	err := postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error {
		_, err := f.ten.Service().CreateStoreTx(ctx, tx, tenancy.NewStore{Slug: "adm-" + strings.ToLower(f.ids.New().String()[16:]),
			Name: "Victim", Currency: "EGP", Locale: "ar", Timezone: "UTC"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCreateAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c, email := f.createAdmin(t)
	if c.Admin.Email != email || !c.Admin.Active || len(c.Secret) != 32 || !strings.HasPrefix(c.URI, "otpauth://totp/iftahonline%20admin:") {
		t.Fatalf("created = %+v", c)
	}
	if _, err := f.cli.CreateAdmin(t.Context(), email, "Dup", adminPW); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	for name, args := range map[string][3]string{
		"bad email":      {"nope", "A", adminPW},
		"empty name":     {"x@y.co", " ", adminPW},
		"short password": {"x@y.co", "A", "13 chars here"},
	} {
		if _, err := f.cli.CreateAdmin(t.Context(), args[0], args[1], args[2]); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var sealed []byte
	if err := f.owner.QueryRow(t.Context(), "SELECT totp_secret FROM platform_admins WHERE id = $1", c.Admin.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), c.Secret) {
		t.Fatal("TOTP seed must be stored sealed")
	}
}

func TestLoginNeedsPasswordAndTOTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c, email := f.createAdmin(t)
	now := time.Now()

	for name, try := range map[string][3]string{
		"no code":        {email, adminPW, ""},
		"wrong code":     {email, adminPW, "000000"},
		"wrong password": {email, "wrong admin passphrase", code(t, c.Secret, now)},
		"unknown email":  {"nobody@iftahonline.com", adminPW, code(t, c.Secret, now)},
	} {
		if _, err := f.svc.Login(f.device(t), try[0], try[1], try[2]); !errs.Is(err, errs.KindUnauthenticated) && !errs.Is(err, errs.KindRateLimited) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Three failures for this email lock it, even with everything right.
	if _, err := f.svc.Login(f.device(t), email, adminPW, code(t, c.Secret, now)); !errs.Is(err, errs.KindRateLimited) {
		t.Fatalf("locked: %v", err)
	}

	g := newFixture(t)
	c2, email2 := g.createAdmin(t)
	res, err := g.svc.Login(g.device(t), email2, adminPW, code(t, c2.Secret, now))
	if err != nil || res.Token == "" || time.Until(res.Expires) > 31*time.Minute {
		t.Fatalf("login = %+v, %v", res, err)
	}
	// The same code cannot be replayed for a second session.
	if _, err = g.svc.Login(g.device(t), email2, adminPW, code(t, c2.Secret, now)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("replayed code: %v", err)
	}
	// A disabled admin cannot sign in or use a live session.
	id, err := g.svc.Authenticate(t.Context(), string(res.Token))
	if err != nil || id.AdminID != c2.Admin.ID {
		t.Fatalf("authenticate = %+v, %v", id, err)
	}
	if _, err := g.owner.Exec(t.Context(), "UPDATE platform_admins SET status = 'disabled' WHERE id = $1", c2.Admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.Authenticate(t.Context(), string(res.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disabled session: %v", err)
	}
}

func (f fixture) signedIn(t *testing.T) (context.Context, app.Created) {
	t.Helper()
	c, email := f.createAdmin(t)
	res, err := f.svc.Login(f.device(t), email, adminPW, code(t, c.Secret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.svc.Authenticate(t.Context(), string(res.Token))
	if err != nil {
		t.Fatal(err)
	}
	return app.WithIdentity(f.device(t), id), c
}

func TestSuspendAndReactivate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	store := f.createStore(t)

	if _, err := f.svc.Suspend(ctx, store, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("short reason: %v", err)
	}
	st, err := f.svc.Suspend(ctx, store, "chargeback fraud, ticket #77")
	if err != nil || st.Status != tenancy.StatusSuspended {
		t.Fatalf("suspend = %+v, %v", st, err)
	}
	if _, err = f.svc.Suspend(ctx, store, "again please"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("suspend twice: %v", err)
	}
	if _, err = f.svc.Suspend(ctx, tenant.StoreID{ID: f.ids.New()}, "unknown store"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	st, err = f.svc.Reactivate(ctx, store, "resolved with the merchant")
	if err != nil || st.Status != tenancy.StatusActive {
		t.Fatalf("reactivate = %+v, %v", st, err)
	}

	// The store's own audit log shows both changes (system actor), and the
	// admin log shows who did it and why.
	var n int
	err = pgx.BeginFunc(t.Context(), f.owner, func(tx pgx.Tx) error {
		if _, err = tx.Exec(t.Context(), "SELECT set_config('app.store_id', $1::uuid::text, true)", store.ID); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE store_id = $1 AND action = 'store.status_changed' AND actor_user_id IS NULL", store.ID).Scan(&n)
	})
	if err != nil || n != 2 {
		t.Fatalf("store audit entries = %d, %v", n, err)
	}
	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	// The admin log is shared by every admin (and parallel tests): keep ours.
	var actions []string
	suspendMeta := ""
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID {
			actions = append(actions, e.Action)
			if e.Action == "store.suspended" {
				suspendMeta = string(e.Metadata)
			}
		}
	}
	if got := strings.Join(actions, ","); got != "store.reactivated,store.suspended,admin.signed_in" {
		t.Fatalf("admin audit = %s", got)
	}
	if !strings.Contains(suspendMeta, "chargeback fraud") || !strings.Contains(suspendMeta, store.String()) && !strings.Contains(suspendMeta, "slug") {
		t.Fatalf("suspension entry = %s", suspendMeta)
	}
	// The admin log is append-only, even for the console's own role.
	if _, err := f.owner.Exec(t.Context(), "DELETE FROM admin_audit_log"); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("delete admin audit: %v", err)
	}
}

func TestDirectoryAndSessionsNeedIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	store := f.createStore(t)

	st, ds, err := f.svc.Store(ctx, store)
	if err != nil || st.ID != store || len(ds) != 1 {
		t.Fatalf("store = %+v %+v %v", st, ds, err)
	}
	if page, searchErr := f.svc.Stores(ctx, st.Slug, "", "", 10); searchErr != nil || len(page.Items) != 1 {
		t.Fatalf("search = %+v, %v", page, searchErr)
	}
	a, err := f.svc.Me(ctx)
	if err != nil || a.ID != me.Admin.ID || a.LastLoginAt == nil {
		t.Fatalf("me = %+v, %v", a, err)
	}
	if _, err := f.svc.Audit(ctx, "!!", 10); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := f.svc.Audit(ctx, "", 500); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("huge limit: %v", err)
	}
	if p, err := f.svc.Audit(ctx, "", 1); err != nil || len(p.Items) != 1 || p.NextCursor == "" {
		t.Fatalf("paged audit = %+v, %v", p, err)
	} else if p2, err := f.svc.Audit(ctx, p.NextCursor, 1); err != nil || len(p2.Items) != 1 || p2.Items[0].ID == p.Items[0].ID {
		t.Fatalf("second page = %+v, %v", p2, err)
	}

	anon := t.Context()
	for name, call := range map[string]func() error{
		"me":         func() error { _, e := f.svc.Me(anon); return e },
		"stores":     func() error { _, e := f.svc.Stores(anon, "", "", "", 1); return e },
		"store":      func() error { _, _, e := f.svc.Store(anon, store); return e },
		"suspend":    func() error { _, e := f.svc.Suspend(anon, store, "reason here"); return e },
		"audit":      func() error { _, e := f.svc.Audit(anon, "", 1); return e },
		"logout":     func() error { return f.svc.Logout(anon) },
		"bad token":  func() error { _, e := f.svc.Authenticate(anon, "junk"); return e },
		"good shape": func() error { _, e := f.svc.Authenticate(anon, strings.Repeat("A", 43)); return e },
	} {
		if err := call(); !errs.Is(err, errs.KindUnauthenticated) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.svc.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Me(ctx); err != nil {
		// ctx still carries the identity; the session itself is revoked:
		t.Fatalf("me with stale identity: %v", err)
	}
}
