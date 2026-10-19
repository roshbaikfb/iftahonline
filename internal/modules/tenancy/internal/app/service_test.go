package app_test

import (
	"context"
	"errors"
	"net"
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
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

const base = "iftahonline.com"

type fixture struct {
	app   *pgxpool.Pool // app_user, NOBYPASSRLS
	owner *pgxpool.Pool
	rdb   *redis.Client
	svc   *app.Service
	ids   *ulid.Generator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	appPool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(appPool.Close)
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
	ins, err := river.NewClient(riverpgxv5.New(appPool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	return fixture{app: appPool, owner: owner, rdb: rdb, svc: app.NewService(appPool, ins, ids, base, audit.Nop{}), ids: ids}
}

// slug returns a unique valid slug (tests share one database).
func (f fixture) slug(prefix string) string {
	return prefix + "-" + strings.ToLower(f.ids.New().String()[16:])
}

// createStore signs a store up the way identity will: a fresh ID as the
// tenant, CreateStoreTx inside InTenantTx.
func (f fixture) createStore(t *testing.T, slug string) (context.Context, domain.Store) {
	t.Helper()
	ctx := tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()})
	var st domain.Store
	err := postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error {
		var err error
		st, err = f.svc.CreateStoreTx(ctx, tx, domain.NewStore{
			Slug: slug, Name: "متجر " + slug, Currency: "EGP", Locale: "ar", Timezone: "Africa/Cairo",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, st
}

// jobsFor returns the args of jobs of kind enqueued for store (as owner,
// so RLS on our tables does not matter; River tables have none).
func (f fixture) jobsFor(t *testing.T, kind string, store tenant.StoreID) []string {
	t.Helper()
	rows, err := f.owner.Query(t.Context(),
		`SELECT args::text FROM river_job WHERE kind = $1 AND args->>'store_id' = $2 ORDER BY id`, kind, store.String())
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func kindOf(err error) errs.Kind { return errs.KindOf(err) }

func codeOf(err error) string {
	// Validation errors carry the specific code on the field.
	if e, ok := errors.AsType[*errs.Error](err); ok {
		if len(e.Fields) > 0 {
			return e.Fields[0].Code
		}
		return e.Code
	}
	return ""
}

func TestCreateStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	slug := f.slug("shop")
	ctx, st := f.createStore(t, slug)

	if st.Slug != slug || st.Status != domain.StatusActive || st.Plan != domain.PlanFree || st.Currency != "EGP" {
		t.Fatalf("store = %+v", st)
	}
	ds, err := f.svc.ListDomains(ctx)
	if err != nil || len(ds) != 1 {
		t.Fatalf("domains = %+v, %v", ds, err)
	}
	d := ds[0]
	if d.Host != slug+"."+base || d.Kind != domain.KindSubdomain || d.Status != domain.DomainVerified || !d.IsPrimary || d.VerifiedAt == nil {
		t.Fatalf("subdomain = %+v", d)
	}
	// Two invalidations (now and delayed) clear any negative cache entry.
	if got := f.jobsFor(t, "tenancy.hosts.invalidate", st.ID); len(got) != 2 || !strings.Contains(got[0], d.Host) {
		t.Fatalf("invalidation jobs = %v", got)
	}

	// The slug is taken now, in any case.
	ctx2 := tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()})
	err = postgres.InTenantTx(ctx2, f.app, func(tx pgx.Tx) error {
		_, txErr := f.svc.CreateStoreTx(ctx2, tx, domain.NewStore{Slug: strings.ToUpper(slug), Name: "x", Currency: "SAR", Locale: "en", Timezone: "UTC"})
		return txErr
	})
	if kindOf(err) != errs.KindValidation || codeOf(err) != "taken" {
		t.Fatalf("duplicate slug: %v", err)
	}
}

func TestCreateStoreValidatesAndNeedsTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()})
	err := postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error {
		_, err := f.svc.CreateStoreTx(ctx, tx, domain.NewStore{Slug: "admin", Name: "x", Currency: "EGP", Locale: "ar", Timezone: "UTC"})
		return err
	})
	if codeOf(err) != "reserved" {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.GetStore(t.Context()); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("no tenant: %v", err)
	}
}

func TestStoreSettingsAndStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, st := f.createStore(t, f.slug("settings"))

	got, err := f.svc.GetStore(ctx)
	if err != nil || got.ID != st.ID {
		t.Fatalf("get = %+v, %v", got, err)
	}
	upd, err := f.svc.UpdateSettings(ctx, domain.StoreSettings{Name: " Renamed ", Locale: "en", Timezone: "Asia/Riyadh"})
	if err != nil || upd.Name != "Renamed" || upd.Locale != "en" || upd.Timezone != "Asia/Riyadh" || !upd.UpdatedAt.After(st.UpdatedAt) {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	if _, err = f.svc.UpdateSettings(ctx, domain.StoreSettings{Name: ""}); kindOf(err) != errs.KindValidation {
		t.Fatalf("invalid update: %v", err)
	}

	before := len(f.jobsFor(t, "tenancy.hosts.invalidate", st.ID))
	sus, err := f.svc.SetStatus(ctx, domain.StatusSuspended)
	if err != nil || sus.Status != domain.StatusSuspended {
		t.Fatalf("suspend = %+v, %v", sus, err)
	}
	if after := len(f.jobsFor(t, "tenancy.hosts.invalidate", st.ID)); after != before+2 {
		t.Fatalf("suspension must invalidate hosts: %d -> %d jobs", before, after)
	}
	if _, err := f.svc.SetStatus(ctx, "deleted"); kindOf(err) != errs.KindValidation {
		t.Fatalf("bad status: %v", err)
	}

	// A store ID that does not exist (or is another tenant's) is not found.
	ghost := tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()})
	for name, fn := range map[string]func() error{
		"get": func() error { _, err := f.svc.GetStore(ghost); return err },
		"update": func() error {
			_, err := f.svc.UpdateSettings(ghost, domain.StoreSettings{Name: "x", Locale: "ar", Timezone: "UTC"})
			return err
		},
		"status": func() error { _, err := f.svc.SetStatus(ghost, domain.StatusClosed); return err },
	} {
		if err := fn(); kindOf(err) != errs.KindNotFound {
			t.Fatalf("%s on missing store: %v", name, err)
		}
	}
}

func TestCustomDomainLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, st := f.createStore(t, f.slug("domains"))
	host := f.slug("brand") + ".example.com"

	d, err := f.svc.AddCustomDomain(ctx, " "+strings.ToUpper(host)+" ")
	if err != nil {
		t.Fatal(err)
	}
	if d.Host != host || d.Status != domain.DomainPending || d.Kind != domain.KindCustom || d.IsPrimary || len(d.VerificationToken) != 32 {
		t.Fatalf("domain = %+v", d)
	}
	if got := f.jobsFor(t, "tenancy.domain.verify", st.ID); len(got) != 1 || !strings.Contains(got[0], d.ID.String()) {
		t.Fatalf("verify jobs = %v", got)
	}
	if _, err := f.svc.AddCustomDomain(ctx, host); codeOf(err) != "domain_exists" {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := f.svc.AddCustomDomain(ctx, "x."+base); codeOf(err) != "reserved" {
		t.Fatalf("platform domain: %v", err)
	}
	if _, err := f.svc.SetPrimaryDomain(ctx, d.ID); codeOf(err) != "domain_not_verified" {
		t.Fatalf("primary before verify: %v", err)
	}

	// Asking again while a check is live does not stack jobs (unique).
	if _, err := f.svc.VerifyDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.jobsFor(t, "tenancy.domain.verify", st.ID); len(got) != 1 {
		t.Fatalf("verify jobs after re-request = %d, want 1", len(got))
	}

	ds, _ := f.svc.ListDomains(ctx)
	var sub domain.Domain
	for _, x := range ds {
		if x.Kind == domain.KindSubdomain {
			sub = x
		}
	}
	if err := f.svc.RemoveDomain(ctx, sub.ID); codeOf(err) != "cannot_remove_subdomain" {
		t.Fatalf("remove subdomain: %v", err)
	}
	if err := f.svc.RemoveDomain(ctx, f.ids.New()); kindOf(err) != errs.KindNotFound {
		t.Fatalf("remove missing: %v", err)
	}
	if err := f.svc.RemoveDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if ds, _ := f.svc.ListDomains(ctx); len(ds) != 1 {
		t.Fatalf("domains after remove = %d", len(ds))
	}
	if _, err := f.svc.VerifyDomain(ctx, d.ID); kindOf(err) != errs.KindNotFound {
		t.Fatalf("verify removed: %v", err)
	}
	if _, err := f.svc.SetPrimaryDomain(ctx, d.ID); kindOf(err) != errs.KindNotFound {
		t.Fatalf("primary removed: %v", err)
	}
}

func TestCustomDomainLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.createStore(t, f.slug("limit"))
	for i := range domain.MaxCustomDomains {
		if _, err := f.svc.AddCustomDomain(ctx, f.slug("d")+".example.com"); err != nil {
			t.Fatalf("domain %d: %v", i, err)
		}
	}
	if _, err := f.svc.AddCustomDomain(ctx, f.slug("d")+".example.com"); codeOf(err) != "domain_limit" {
		t.Fatalf("over limit: %v", err)
	}
}

// fakeDNS answers TXT lookups from a map; missing names are NXDOMAIN.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string][]string
	fail    bool
}

func (d *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return nil, &net.DNSError{Err: "server misbehaving", Name: name, IsTemporary: true}
	}
	v, ok := d.records[name]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return v, nil
}

func (d *fakeDNS) publish(dm domain.Domain) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name, value := dm.VerificationRecord()
	d.records[name] = []string{"v=spf1 -all", value}
}

func work(ctx context.Context, w *app.VerifyWorker, d domain.Domain, attempt int) error {
	return w.Work(ctx, &river.Job[app.VerifyDomainArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: app.VerifyMaxAttempts},
		Args:   app.VerifyDomainArgs{TenantArgs: jobs.TenantArgs{StoreID: d.StoreID}, DomainID: d.ID},
	})
}

func getDomain(ctx context.Context, t *testing.T, f fixture, id ulid.ID) domain.Domain {
	t.Helper()
	var d domain.Domain
	err := postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error {
		var err error
		d, err = store.GetDomain(ctx, tx, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestVerifyWorker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dns := &fakeDNS{records: map[string][]string{}}
	w := &app.VerifyWorker{DB: f.app, DNS: dns, Svc: f.svc, LookupTimeout: time.Second}
	if w.Timeout(nil) <= 0 {
		t.Fatal("worker needs a timeout")
	}

	ctxA, _ := f.createStore(t, f.slug("verify-a"))
	ctxB, stB := f.createStore(t, f.slug("verify-b"))
	host := f.slug("contested") + ".example.com"
	dA, err := f.svc.AddCustomDomain(ctxA, host)
	if err != nil {
		t.Fatal(err)
	}
	dB, err := f.svc.AddCustomDomain(ctxB, host) // pending claims coexist
	if err != nil {
		t.Fatal(err)
	}

	// No record yet: retry later, reason recorded.
	if err = work(ctxB, w, dB, 1); err == nil {
		t.Fatal("missing TXT must be retried")
	}
	if got := getDomain(ctxB, t, f, dB.ID); got.Status != domain.DomainPending || got.FailureReason != domain.ReasonTXTNotFound || got.CheckAttempts != 1 {
		t.Fatalf("after miss = %+v", got)
	}
	dns.fail = true
	if err = work(ctxB, w, dB, 2); err == nil {
		t.Fatal("DNS failure must be retried")
	}
	if got := getDomain(ctxB, t, f, dB.ID); got.FailureReason != domain.ReasonDNSError {
		t.Fatalf("after DNS error = %+v", got)
	}
	dns.fail = false

	// A publishes B's token? No: each store's token is its own. B proves
	// ownership first and wins.
	dns.publish(dB)
	before := len(f.jobsFor(t, "tenancy.hosts.invalidate", stB.ID))
	if err = work(ctxB, w, dB, 3); err != nil {
		t.Fatal(err)
	}
	if got := getDomain(ctxB, t, f, dB.ID); got.Status != domain.DomainVerified || got.VerifiedAt == nil || got.FailureReason != "" {
		t.Fatalf("verified = %+v", got)
	}
	if after := len(f.jobsFor(t, "tenancy.hosts.invalidate", stB.ID)); after != before+2 {
		t.Fatalf("verification must invalidate the host: %d -> %d", before, after)
	}
	// Running again is a no-op.
	if err = work(ctxB, w, dB, 4); err != nil {
		t.Fatal(err)
	}

	// A's token also appears (A controls DNS too, say), but B verified
	// first: A's claim fails as taken.
	dns.publish(dA)
	if err = work(ctxA, w, dA, 1); err != nil {
		t.Fatal(err)
	}
	if got := getDomain(ctxA, t, f, dA.ID); got.Status != domain.DomainFailed || got.FailureReason != domain.ReasonTaken {
		t.Fatalf("contested = %+v", got)
	}

	// B can now make it primary; the old subdomain stops being primary.
	p, err := f.svc.SetPrimaryDomain(ctxB, dB.ID)
	if err != nil || !p.IsPrimary {
		t.Fatalf("primary = %+v, %v", p, err)
	}
	if err = f.svc.RemoveDomain(ctxB, dB.ID); codeOf(err) != "cannot_remove_primary" {
		t.Fatalf("remove primary: %v", err)
	}
	if _, err = f.svc.VerifyDomain(ctxB, dB.ID); codeOf(err) != "domain_verified" {
		t.Fatalf("re-verify verified: %v", err)
	}

	// The last attempt gives up.
	ctxC, _ := f.createStore(t, f.slug("verify-c"))
	dC, err := f.svc.AddCustomDomain(ctxC, f.slug("never")+".example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := work(ctxC, w, dC, app.VerifyMaxAttempts); err != nil {
		t.Fatalf("final attempt must not error: %v", err)
	}
	if got := getDomain(ctxC, t, f, dC.ID); got.Status != domain.DomainFailed || got.FailureReason != domain.ReasonTimedOut {
		t.Fatalf("timed out = %+v", got)
	}
	// ...and the merchant can restart it.
	if got, err := f.svc.VerifyDomain(ctxC, dC.ID); err != nil || got.Status != domain.DomainPending || got.CheckAttempts != 0 {
		t.Fatalf("restart = %+v, %v", got, err)
	}

	// A deleted domain ends the job quietly.
	if err := f.svc.RemoveDomain(ctxC, dC.ID); err != nil {
		t.Fatal(err)
	}
	if err := work(ctxC, w, dC, 1); err != nil {
		t.Fatalf("deleted domain: %v", err)
	}
}

// TestCrossTenantIsolation: store A, holding B's resource IDs, can neither
// read nor change them, and a raw query without a tenant sees nothing.
func TestCrossTenantIsolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctxA, _ := f.createStore(t, f.slug("iso-a"))
	ctxB, stB := f.createStore(t, f.slug("iso-b"))
	dB, err := f.svc.AddCustomDomain(ctxB, f.slug("b")+".example.com")
	if err != nil {
		t.Fatal(err)
	}

	if err = f.svc.RemoveDomain(ctxA, dB.ID); kindOf(err) != errs.KindNotFound {
		t.Fatalf("A removes B's domain: %v", err)
	}
	if _, err = f.svc.VerifyDomain(ctxA, dB.ID); kindOf(err) != errs.KindNotFound {
		t.Fatalf("A verifies B's domain: %v", err)
	}
	if _, err = f.svc.SetPrimaryDomain(ctxA, dB.ID); kindOf(err) != errs.KindNotFound {
		t.Fatalf("A promotes B's domain: %v", err)
	}
	dsA, _ := f.svc.ListDomains(ctxA)
	for _, d := range dsA {
		if d.StoreID == stB.ID {
			t.Fatal("A lists B's domain")
		}
	}
	if got := getDomain(ctxB, t, f, dB.ID); got.Status != domain.DomainPending {
		t.Fatalf("B's domain changed: %+v", got)
	}

	// A job for B's domain enqueued from A's context is refused.
	ins, _ := river.NewClient(riverpgxv5.New(f.app), &river.Config{Middleware: jobs.Middleware()})
	err = postgres.InTenantTx(ctxA, f.app, func(tx pgx.Tx) error {
		_, insErr := ins.InsertTx(ctxA, tx, app.VerifyDomainArgs{TenantArgs: jobs.TenantArgs{StoreID: stB.ID}, DomainID: dB.ID}, nil)
		return insErr
	})
	if !errors.Is(err, jobs.ErrTenantMismatch) {
		t.Fatalf("cross-tenant job insert: %v", err)
	}

	// A raw query with no tenant filter returns 0 rows (second wall).
	testkit.AssertNoRowsWithoutTenant(t, f.app)
	var n int
	if err = f.app.QueryRow(t.Context(), "SELECT count(*) FROM stores").Scan(&n); err != nil || n != 0 {
		t.Fatalf("stores visible without tenant: %d, %v", n, err)
	}
	// Even inside A's transaction, B's rows are invisible to a raw query.
	err = postgres.InTenantTx(ctxA, f.app, func(tx pgx.Tx) error {
		return tx.QueryRow(ctxA, "SELECT count(*) FROM store_domains WHERE store_id = $1", stB.ID.ID).Scan(&n)
	})
	if err != nil || n != 0 {
		t.Fatalf("B's domains visible in A's transaction: %d, %v", n, err)
	}
	// And A cannot write a row claiming to be B's.
	err = postgres.InTenantTx(ctxA, f.app, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctxA, `INSERT INTO store_domains (id, store_id, host, kind, status) VALUES ($1, $2, 'evil.example.com', 'custom', 'pending')`,
			f.ids.New(), stB.ID.ID)
		return execErr
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("write as B from A: %v", err)
	}
}

func TestStoreDirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	dir := app.NewStoreDirectory(adminPool)
	tag := f.slug("sd")
	_, a := f.createStore(t, tag+"-a")
	_, b := f.createStore(t, tag+"-b")

	page, err := dir.Search(t.Context(), tag, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Store.ID != b.ID || page.NextCursor == "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	next, err := dir.Search(t.Context(), tag, "active", page.NextCursor, 0)
	if err != nil || len(next.Items) != 1 || next.Items[0].Store.ID != a.ID || next.NextCursor != "" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	for name, call := range map[string]func() error{
		"limit":   func() error { _, e := dir.Search(t.Context(), "", "", "", 101); return e },
		"query":   func() error { _, e := dir.Search(t.Context(), strings.Repeat("q", 101), "", "", 10); return e },
		"status":  func() error { _, e := dir.Search(t.Context(), "", "gone", "", 10); return e },
		"cursor":  func() error { _, e := dir.Search(t.Context(), "", "", "bm8", 10); return e },
		"cursor2": func() error { _, e := dir.Search(t.Context(), "", "", "eDp5", 10); return e },
	} {
		if e := call(); kindOf(e) != errs.KindValidation {
			t.Errorf("%s: %v", name, e)
		}
	}
	st, ds, err := dir.Store(t.Context(), a.ID)
	if err != nil || st.ID != a.ID || len(ds) != 1 {
		t.Fatalf("store = %+v %+v %v", st, ds, err)
	}
	if _, _, err := dir.Store(t.Context(), tenant.StoreID{ID: f.ids.New()}); kindOf(err) != errs.KindNotFound {
		t.Fatalf("missing store: %v", err)
	}
}
