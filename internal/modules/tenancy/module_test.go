package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// redisCounter counts commands sent to Redis.
type redisCounter struct{ n atomic.Int64 }

func (c *redisCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *redisCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmd)
	}
}

func (c *redisCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

type instance struct {
	mod *tenancy.Module
	qc  *testkit.QueryCounter
	rc  *redisCounter
}

func cfg() config.Tenancy {
	return config.Tenancy{
		BaseDomain: "iftahonline.com", HostCacheSize: 100, HostCacheTTL: time.Minute,
		NegativeCacheSize: 100, NegativeTTL: time.Minute, SharedCacheTTL: time.Minute,
	}
}

func newInstance(t *testing.T) instance {
	t.Helper()
	pool, qc := testkit.CountingPool(t, infra.AppURL)
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	rc := &redisCounter{}
	rdb.AddHook(rc)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	mod, err := tenancy.New(tenancy.Deps{
		Pool: pool, Redis: rdb, Jobs: ins, IDs: ulid.NewGenerator(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Config: cfg(),
		DNS: &net.Resolver{PreferGo: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return instance{mod: mod, qc: qc, rc: rc}
}

func TestNewRequiresDeps(t *testing.T) {
	t.Parallel()
	if _, err := tenancy.New(tenancy.Deps{}); err == nil {
		t.Fatal("missing deps must fail")
	}
}

// TestHostResolutionEndToEnd exercises ADR-0004 on real PostgreSQL and
// Redis: a miss costs one query, a hit costs zero queries and zero Redis
// commands, a second instance is served from Redis, and a status change
// reaches every instance through River and pub/sub.
func TestHostResolutionEndToEnd(t *testing.T) {
	ctx := t.Context()
	a, b := newInstance(t), newInstance(t)

	// Sign a store up.
	slug := "e2e-" + strings.ToLower(ulid.NewGenerator().New().String()[18:])
	host := slug + ".iftahonline.com"
	storeCtx := tenant.WithStore(ctx, tenant.StoreID{ID: ulid.NewGenerator().New()})
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var st tenancy.Store
	err = postgres.InTenantTx(storeCtx, pool, func(tx pgx.Tx) error {
		st, err = a.mod.Service().CreateStoreTx(storeCtx, tx, tenancy.NewStore{
			Slug: slug, Name: "E2E", Currency: "AED", Locale: "en", Timezone: "Asia/Dubai",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Miss on A: one database query.
	a.qc.Reset()
	res, err := a.mod.Resolver().Resolve(ctx, host)
	if err != nil || res.StoreID != st.ID || res.Status != tenancy.StatusActive {
		t.Fatalf("resolve = %+v, %v", res, err)
	}
	if a.qc.Count() != 1 {
		t.Fatalf("miss queries = %d, want 1", a.qc.Count())
	}

	// Hit on A: zero queries, zero Redis commands.
	a.qc.Reset()
	redisBefore := a.rc.n.Load()
	for range 50 {
		if _, err = a.mod.Resolver().Resolve(ctx, strings.ToUpper(host)+":443"); err != nil {
			t.Fatal(err)
		}
	}
	if a.qc.Count() != 0 || a.rc.n.Load() != redisBefore {
		t.Fatalf("hits did I/O: %d queries, %d redis commands", a.qc.Count(), a.rc.n.Load()-redisBefore)
	}

	// B has a cold L1 but finds the host in Redis: zero queries.
	b.qc.Reset()
	if res, err = b.mod.Resolver().Resolve(ctx, host); err != nil || res.StoreID != st.ID {
		t.Fatalf("B resolve = %+v, %v", res, err)
	}
	if b.qc.Count() != 0 {
		t.Fatalf("L2 hit queries = %d, want 0", b.qc.Count())
	}

	// Unknown host: negative everywhere, then cached.
	if _, err = b.mod.Resolver().Resolve(ctx, "nobody-"+slug+".iftahonline.com"); !errors.Is(err, tenancy.ErrUnknownHost) {
		t.Fatalf("unknown host: %v", err)
	}

	// Both instances listen for invalidations; a worker runs the jobs.
	lctx, stop := context.WithCancel(ctx)
	defer stop()
	go a.mod.ListenInvalidations(lctx)
	go b.mod.ListenInvalidations(lctx)
	time.Sleep(100 * time.Millisecond) // let SUBSCRIBE land

	workers := river.NewWorkers()
	if err = a.mod.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	workerPool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerPool.Close)
	client, err := river.NewClient(riverpgxv5.New(workerPool), &river.Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}}, Workers: workers,
		Middleware: jobs.Middleware(), FetchPollInterval: 100 * time.Millisecond, TestOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = client.Stop(sctx)
	})

	if _, err := a.mod.Service().SetStatus(storeCtx, tenancy.StatusSuspended); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]instance{"A": a, "B": b} {
		deadline := time.Now().Add(10 * time.Second)
		for {
			res, err := in.mod.Resolver().Resolve(ctx, host)
			if err == nil && res.Status == tenancy.StatusSuspended {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("instance %s still serves %+v, %v after suspension", name, res, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestAdminDirectory: the console's app_admin pool sees every store,
// read-only; the same queries as app_user see nothing (RLS).
func TestAdminDirectory(t *testing.T) {
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	a := newInstance(t)
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opts, _ := redis.ParseURL(infra.RedisURL)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ins, _ := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	mod, err := tenancy.New(tenancy.Deps{Pool: pool, Redis: rdb, Jobs: ins, IDs: ulid.NewGenerator(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Config: cfg(), AdminPool: admin})
	if err != nil {
		t.Fatal(err)
	}
	if a.mod.Directory() != nil {
		t.Fatal("no admin pool, no directory")
	}
	dir := mod.Directory()

	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	var ids []tenant.StoreID
	for i, name := range []string{"Alpha " + tag, "Beta " + tag, "Gamma " + tag} {
		sctx := tenant.WithStore(ctx, tenant.StoreID{ID: ulid.NewGenerator().New()})
		txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, createErr := mod.Service().CreateStoreTx(sctx, tx, tenancy.NewStore{
				Slug: fmt.Sprintf("dir%d-%s", i, tag), Name: name, Currency: "EGP", Locale: "ar", Timezone: "UTC"})
			return createErr
		})
		if txErr != nil {
			t.Fatal(txErr)
		}
		id, _ := tenant.FromContext(sctx)
		ids = append(ids, id)
	}
	if _, err = mod.Service().SetStatus(tenant.WithStore(ctx, ids[1]), tenancy.StatusSuspended); err != nil {
		t.Fatal(err)
	}

	page, err := dir.Search(ctx, tag, "", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" || page.Items[0].Store.ID != ids[2] ||
		page.Items[0].PrimaryHost != fmt.Sprintf("dir2-%s.iftahonline.com", tag) {
		t.Fatalf("page 1 = %+v, %v", page, err)
	}
	page2, err := dir.Search(ctx, tag, "", page.NextCursor, 2)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].Store.ID != ids[0] || page2.NextCursor != "" {
		t.Fatalf("page 2 = %+v, %v", page2, err)
	}
	if p, _ := dir.Search(ctx, tag, "suspended", "", 10); len(p.Items) != 1 || p.Items[0].Store.ID != ids[1] {
		t.Fatalf("status filter = %+v", p)
	}
	if p, _ := dir.Search(ctx, "dir1-"+tag, "", "", 10); len(p.Items) != 1 {
		t.Fatalf("slug prefix = %+v", p)
	}
	// LIKE wildcards in the query are literal.
	if p, _ := dir.Search(ctx, "%"+tag, "", "", 10); len(p.Items) != 0 {
		t.Fatalf("wildcard must be literal, got %d", len(p.Items))
	}
	for _, bad := range []struct{ status, cursor string }{{"deleted", ""}, {"", "!!"}} {
		if _, err = dir.Search(ctx, "", bad.status, bad.cursor, 10); err == nil {
			t.Fatalf("bad input %+v accepted", bad)
		}
	}
	st, ds, err := dir.Store(ctx, ids[1])
	if err != nil || st.Status != tenancy.StatusSuspended || len(ds) != 1 || !ds[0].IsPrimary {
		t.Fatalf("store = %+v %+v %v", st, ds, err)
	}
	if _, _, err := dir.Store(ctx, tenant.StoreID{ID: ulid.NewGenerator().New()}); err == nil {
		t.Fatal("unknown store must be not found")
	}

	// app_admin is read-only on the directory and blind elsewhere.
	for _, q := range []string{
		"UPDATE stores SET name = 'x'", // privileges are checked before any row
		"SELECT count(*) FROM audit_log",
		"SELECT count(*) FROM users",
		"SELECT count(*) FROM store_memberships",
		"SELECT count(*) FROM user_sessions",
	} {
		if _, err := admin.Exec(ctx, q); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("app_admin: %s: %v", q, err)
		}
	}
	// And app_user sees none of it without a tenant.
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM stores").Scan(&n); err != nil || n != 0 {
		t.Fatalf("app_user sees %d stores, %v", n, err)
	}
	if _, err := pool.Exec(ctx, "SELECT count(*) FROM platform_admins"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("app_user reads platform_admins: %v", err)
	}
}
