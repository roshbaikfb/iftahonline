package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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

// admit runs AdmitTx in its own transaction with a store holding have.
func (f fixture) admit(ctx context.Context, svc *app.Service, feature domain.Feature, have, n int64) (g domain.Grant, err error) {
	err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		g, txErr = svc.AdmitTx(ctx, tx, feature, n, func(context.Context, pgx.Tx) (int64, error) { return have, nil })
		return txErr
	})
	return g, err
}

func TestAdmit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed(planGrants))
	ctx := f.store(t)
	tests := []struct {
		feature domain.Feature
		have, n int64
		err     error
	}{
		{domain.Staff, 2, 1, nil},
		{domain.Staff, 3, 1, domain.ErrLimitReached},
		{domain.Staff, 1, 3, domain.ErrLimitReached},
		{domain.Staff, 9, 1, domain.ErrLimitReached}, // already over, after a downgrade
		{domain.Products, 1 << 40, 1, nil},
		{domain.CustomDomains, 0, 1, domain.ErrLimitReached}, // not listed: none
		{domain.AdFree, 0, 1, domain.ErrInvalid},
		{domain.OrdersPerMonth, 0, 1, domain.ErrInvalid},
		{domain.Staff, 0, 0, domain.ErrInvalid},
	}
	for _, tt := range tests {
		g, err := f.admit(ctx, f.svc, tt.feature, tt.have, tt.n)
		if !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
			t.Errorf("AdmitTx(%s, have %d, +%d): err = %v, want %v", tt.feature, tt.have, tt.n, err, tt.err)
		}
		if errors.Is(err, domain.ErrLimitReached) && (errs.KindOf(err) != errs.KindForbidden || !strings.Contains(err.Error(), domain.PlanLimitCode)) {
			t.Errorf("AdmitTx(%s): refusal %v is not a plan_limit 403", tt.feature, err)
		}
		if tt.feature == domain.Staff && tt.n > 0 && g.Value != 3 {
			t.Errorf("AdmitTx(%s) grant = %+v", tt.feature, g)
		}
	}
}

// TestAdmitRace: 20 concurrent creations against a limit of 3 make 3.
func TestAdmitRace(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.Fixed{domain.Staff: {Value: 3}, domain.Messages: {Unlimited: true}})
	ctx := f.store(t)
	var made atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
				// The "creation" is a messages unit; the count reads them back.
				count := func(ctx context.Context, tx pgx.Tx) (int64, error) {
					u, err := f.svc.Usage(ctx, domain.Messages)
					_ = tx
					return u.Used, err
				}
				if _, err := f.svc.AdmitTx(ctx, tx, domain.Staff, 1, count); err != nil {
					return err
				}
				_, err := f.svc.ConsumeTx(ctx, tx, domain.Messages, 1)
				return err
			})
			switch {
			case err == nil:
				made.Add(1)
			case !errors.Is(err, domain.ErrLimitReached):
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if made.Load() != 3 {
		t.Fatalf("made %d, want 3", made.Load())
	}
}

// TestCountTx: a soft monthly limit counts past the limit and tells the
// merchant at 80% and 100%, once each.
func TestCountTx(t *testing.T) {
	t.Parallel()
	events := testkit.NewEvents()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc := app.NewService(app.Deps{DB: pool, Source: app.Fixed{domain.OrdersPerMonth: {Value: 5}}, Events: events})
	ctx := newFixture(t, app.Fixed{}).store(t)
	for i := int64(1); i <= 7; i++ {
		var u domain.Usage
		if err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) (txErr error) {
			u, txErr = svc.CountTx(ctx, tx, domain.OrdersPerMonth, 1)
			return txErr
		}); err != nil || u.Used != i || u.Limit != 5 {
			t.Fatalf("count %d = %+v, %v", i, u, err)
		}
	}
	all := events.All()
	if len(all) != 2 {
		t.Fatalf("%d events, want 2 (80%%, 100%%)", len(all))
	}
	for i, want := range []domain.Threshold{domain.Nearly, domain.Reached} {
		ev, ok := all[i].(domain.UsageCrossed)
		if !ok || ev.Threshold != want || ev.Feature != domain.OrdersPerMonth || ev.Limit != 5 || ev.Type != domain.EventUsageCrossed {
			t.Errorf("event %d = %+v", i, all[i])
		}
	}
	if _, err = svc.CountTx(ctx, nil, domain.Staff, 1); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("a counted feature is not monthly: %v", err)
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
		if _, err := svc.Allowed(a, domain.Feeds); err != nil {
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

// TestUnrestricted: what a module built without entitlements gets allows
// everything and touches no database.
func TestUnrestricted(t *testing.T) {
	t.Parallel()
	var u app.Unrestricted
	ctx := t.Context()
	period := domain.PeriodOf(time.Now())
	if ok, err := u.Allowed(ctx, domain.Pixels); err != nil || !ok {
		t.Errorf("Allowed = %v, %v", ok, err)
	}
	if g, err := u.AdmitTx(ctx, nil, domain.Staff, 1, nil); err != nil || !g.Unlimited {
		t.Errorf("AdmitTx = %+v, %v", g, err)
	}
	for _, f := range []func() (domain.Usage, error){
		func() (domain.Usage, error) { return u.CountTx(ctx, nil, domain.OrdersPerMonth, 1) },
		func() (domain.Usage, error) { return u.ConsumeTx(ctx, nil, domain.Messages, 1) },
		func() (domain.Usage, error) { return u.ReleaseTx(ctx, nil, domain.Messages, period, 1) },
		func() (domain.Usage, error) { return u.Usage(ctx, domain.Messages) },
	} {
		if got, err := f(); err != nil || !got.Unlimited() || !got.Period.Equal(period) {
			t.Errorf("usage = %+v, %v", got, err)
		}
	}
}
