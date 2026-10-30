package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/storecache"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// countingSource counts its reads, and checks each one runs in a tenant
// transaction of the store asked about.
type countingSource struct {
	grants domain.Grants
	reads  atomic.Int64
}

func (s *countingSource) GrantsTx(ctx context.Context, tx pgx.Tx) (domain.Grants, error) {
	s.reads.Add(1)
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	var store ulid.ID
	if err = tx.QueryRow(ctx, `SELECT app.current_store_id()`).Scan(&store); err != nil {
		return nil, err
	}
	if store != id.ID {
		return nil, errors.New("read outside the store's transaction")
	}
	return s.grants, nil
}

var planGrants = domain.Grants{
	domain.AdFree:         {Value: 1},
	domain.Pixels:         {Value: 0},
	domain.Feeds:          {Unlimited: true},
	domain.Staff:          {Value: 3},
	domain.Products:       {Unlimited: true},
	domain.OrdersPerMonth: {Unlimited: true},
	domain.Messages:       {Value: 2},
}

func TestAllowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed(planGrants))
	ctx := f.store(t)
	for feature, want := range map[domain.Feature]bool{
		domain.AdFree: true, domain.Pixels: false, domain.Feeds: true, domain.Coupons: false, // not listed: not granted
	} {
		if got, err := f.svc.Allowed(ctx, feature); err != nil || got != want {
			t.Errorf("Allowed(%s) = %v, %v; want %v", feature, got, err, want)
		}
	}
	for _, feature := range []domain.Feature{domain.Staff, domain.Messages, "sms"} {
		if _, err := f.svc.Allowed(ctx, feature); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Allowed(%s): err = %v, want ErrInvalid", feature, err)
		}
	}
	if _, err := f.svc.Allowed(t.Context(), domain.AdFree); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("no store: err = %v", err)
	}
}

func TestCheckCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed(planGrants))
	ctx := f.store(t)
	tests := []struct {
		feature domain.Feature
		after   int64
		err     error
	}{
		{domain.Staff, 3, nil},
		{domain.Staff, 4, domain.ErrLimitReached},
		{domain.Products, 1 << 40, nil},
		{domain.CustomDomains, 1, domain.ErrLimitReached}, // not listed: none
		{domain.CustomDomains, 0, nil},
		{domain.AdFree, 1, domain.ErrInvalid},
		{domain.OrdersPerMonth, 1, domain.ErrInvalid},
	}
	for _, tt := range tests {
		g, err := f.svc.CheckCount(ctx, tt.feature, tt.after)
		if !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
			t.Errorf("CheckCount(%s, %d): err = %v, want %v", tt.feature, tt.after, err, tt.err)
		}
		if tt.feature == domain.Staff && g.Value != 3 {
			t.Errorf("CheckCount(%s) grant = %+v", tt.feature, g)
		}
	}
}

func TestUnlimitedQuota(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed(planGrants))
	ctx := f.store(t)
	var u domain.Usage
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		u, txErr = f.svc.ConsumeTx(ctx, tx, domain.OrdersPerMonth, domain.MaxUnits)
		return txErr
	})
	if err != nil || u.Used != domain.MaxUnits || !u.Unlimited() || u.CrossedBy(domain.MaxUnits) != domain.NoThreshold {
		t.Fatalf("unlimited consume = %+v, %v", u, err)
	}
}

// TestGrantsAreCached: a store's grants are read once, then checks cost
// no query until the TTL or an invalidation.
func TestGrantsAreCached(t *testing.T) {
	t.Parallel()
	pool, queries := testkit.CountingPool(t, infra.AppURL)
	src := &countingSource{grants: planGrants}
	svc := app.NewService(app.Deps{DB: pool, Source: src})
	f := newFixture(t, app.Fixed{})
	a, b := f.store(t), f.store(t)

	if ok, err := svc.Allowed(a, domain.AdFree); err != nil || !ok {
		t.Fatal(ok, err)
	}
	queries.Reset()
	for range 100 {
		if _, err := svc.Allowed(a, domain.AdFree); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CheckCount(a, domain.Staff, 2); err != nil {
			t.Fatal(err)
		}
	}
	if n := queries.Count(); n != 0 || src.reads.Load() != 1 {
		t.Fatalf("cached checks: %d queries, %d reads", n, src.reads.Load())
	}
	if _, err := svc.Allowed(b, domain.AdFree); err != nil || src.reads.Load() != 2 {
		t.Fatalf("store B is read on its own: %d reads, %v", src.reads.Load(), err)
	}
	svc.Invalidate(mustStore(a, t))
	if _, err := svc.Allowed(a, domain.AdFree); err != nil || src.reads.Load() != 3 {
		t.Fatalf("after Invalidate: %d reads, %v", src.reads.Load(), err)
	}

	short := app.NewService(app.Deps{DB: pool, Source: src, Cache: storecache.Config{TTL: 20 * time.Millisecond, Stores: 10}})
	if _, err := short.Allowed(a, domain.AdFree); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	before := src.reads.Load()
	if _, err := short.Allowed(a, domain.AdFree); err != nil || src.reads.Load() != before+1 {
		t.Fatalf("after the TTL: %d reads, %v", src.reads.Load()-before, err)
	}
}

func mustStore(ctx context.Context, t *testing.T) tenant.StoreID {
	t.Helper()
	id, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestConsumeReadsGrantsInTheCallersTx: a cache miss inside ConsumeTx
// reads in the caller's transaction, so a pool of one connection does
// not wait for a second.
func TestConsumeReadsGrantsInTheCallersTx(t *testing.T) {
	t.Parallel()
	cfg, err := pgxpool.ParseConfig(infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	src := &countingSource{grants: planGrants}
	svc := app.NewService(app.Deps{DB: pool, Source: src})
	ctx := newFixture(t, app.Fixed{}).store(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, cErr := svc.ConsumeTx(ctx, tx, domain.Messages, 1)
		return cErr
	})
	if err != nil || src.reads.Load() != 1 {
		t.Fatalf("consume on one connection: %v (%d reads)", err, src.reads.Load())
	}
	if u, err := svc.Usage(ctx, domain.Messages); err != nil || u.Used != 1 || u.Limit != 2 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
}

func BenchmarkAllowedCached(b *testing.B) {
	pool, err := pgxpool.New(b.Context(), infra.AppURL)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	svc := app.NewService(app.Deps{DB: pool, Source: app.Fixed(planGrants)})
	ctx := tenant.WithStore(b.Context(), tenant.StoreID{ID: [16]byte{1}})
	if _, err = svc.Allowed(ctx, domain.AdFree); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, err := svc.Allowed(ctx, domain.AdFree); err != nil || !ok {
				b.Fatal(ok, err)
			}
		}
	})
}

// TestFeaturesMatchTheCatalog holds the enforced features and the
// features table (00056) together: same keys, same kinds.
func TestFeaturesMatchTheCatalog(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed{})
	rows, err := f.pool.Query(t.Context(), `SELECT key, kind FROM features`)
	if err != nil {
		t.Fatal(err)
	}
	catalog := map[domain.Feature]domain.Kind{}
	for rows.Next() {
		var key, kind string
		if err = rows.Scan(&key, &kind); err != nil {
			t.Fatal(err)
		}
		catalog[domain.Feature(key)] = domain.Kind(kind)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for feature, kind := range domain.Features() {
		if catalog[feature] != kind {
			t.Errorf("%s: the catalog says %q, the code %q", feature, catalog[feature], kind)
		}
	}
}
