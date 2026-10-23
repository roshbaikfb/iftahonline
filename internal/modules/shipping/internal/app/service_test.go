package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// recorder keeps what was audited and published.
type recorder struct {
	mu      sync.Mutex
	actions []string
	events  []event.Event
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, e.Action)
	return nil
}

func (r *recorder) PublishTx(_ context.Context, _ pgx.Tx, ev event.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.actions), len(r.events)
}

type fixture struct {
	t          testing.TB
	pool       *pgxpool.Pool
	qc         *testkit.QueryCounter
	rec        *recorder
	ids        *ulid.Generator
	ctxA, ctxB context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, qc := testkit.CountingPool(t, infra.AppURL)
	f := fixture{t: t, pool: pool, qc: qc, rec: &recorder{}, ids: ulid.NewGenerator()}
	f.ctxA, f.ctxB = f.mkStore(t), f.mkStore(t)
	return f
}

func (f fixture) service() *app.Service { return f.serviceWith(app.Carriers{fake.Name: fake.New()}) }

func (f fixture) serviceWith(carriers app.Carriers) *app.Service {
	return app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Audit: f.rec, Events: f.rec, Cache: app.DefaultCacheConfig(),
		Carriers: carriers, Sealer: sealer(f.t), PublicURL: "https://app.example.com/",
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: money.EGP, Country: geo.EG}, nil
		}})
}

func sealer(t testing.TB) *envelope.Sealer {
	t.Helper()
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	s, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f fixture) mkStore(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "shp-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func egp(t testing.TB, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func quote(ctx context.Context, t *testing.T, svc *app.Service, gov, city string, sub int64) domain.Quote {
	t.Helper()
	q, err := svc.Quote(ctx, domain.QuoteRequest{Governorate: gov, City: city, Subtotal: egp(t, sub)})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func ptr[T any](v T) *T { return &v }

// TestZonesAndQuotes: a store's zones, settings and city rates drive its
// quotes, every change is audited and published, and this instance's
// cache follows its own writes at once.
func TestZonesAndQuotes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	ctx := f.ctxA

	if q := quote(ctx, t, svc, "EG-ASN", "", 30000); !q.Deliverable || !q.Shipping.IsZero() || !q.CODFee.IsZero() || q.ZoneID != nil {
		t.Fatalf("a store without zones: %+v", q)
	}

	cairo, err := svc.CreateZone(ctx, domain.ZoneInput{Name: "القاهرة الكبرى", Areas: []string{"EG-GZ", "EG-C"}, RateMinor: 5000,
		Cities: []domain.CityInput{{Name: "المعادي", RateMinor: 4000}}})
	if err != nil {
		t.Fatal(err)
	}
	upper, err := svc.CreateZone(ctx, domain.ZoneInput{Name: "الصعيد", Areas: []string{"EG-ASN"}, RateMinor: 9000,
		CODFee: &domain.CODFee{FixedMinor: 2000}, FreeOverMinor: ptr[int64](300000)})
	if err != nil {
		t.Fatal(err)
	}
	if cairo.Position != 0 || upper.Position != 1 || cairo.Version != 1 || strings.Join(cairo.Areas, ",") != "EG-C,EG-GZ" {
		t.Fatalf("zones: %+v %+v", cairo, upper)
	}
	settings, err := svc.SaveSettings(ctx, domain.Settings{CODFee: domain.CODFee{FixedMinor: 1000}, FreeOverMinor: ptr[int64](100000)}, 0)
	if err != nil || settings.Version != 1 {
		t.Fatalf("settings: %+v %v", settings, err)
	}

	for _, tt := range []struct {
		gov, city string
		sub       int64
		ok        bool
		ship, cod int64
	}{
		{"EG-C", "مدينة نصر", 50000, true, 5000, 1000},
		{"EG-C", "المعادى", 50000, true, 4000, 1000},
		{"EG-GZ", "", 100000, true, 0, 1000},
		{"EG-ASN", "", 100000, true, 9000, 2000},
		{"EG-ALX", "", 50000, false, 0, 0},
	} {
		q := quote(ctx, t, svc, tt.gov, tt.city, tt.sub)
		if q.Deliverable != tt.ok || q.Shipping.Amount() != tt.ship || q.CODFee.Amount() != tt.cod {
			t.Errorf("%s/%s/%d: %+v; want ok=%v ship=%d cod=%d", tt.gov, tt.city, tt.sub, q, tt.ok, tt.ship, tt.cod)
		}
	}

	// Moving Giza out of Cairo's zone frees it; updating keeps position.
	cairo, err = svc.UpdateZone(ctx, cairo.ID, domain.ZoneInput{Name: "القاهرة", Areas: []string{"EG-C"}, RateMinor: 4500}, cairo.Version)
	if err != nil || cairo.Version != 2 || cairo.Position != 0 || len(cairo.Cities) != 0 {
		t.Fatalf("update: %+v %v", cairo, err)
	}
	if q := quote(ctx, t, svc, "EG-C", "المعادي", 50000); q.Shipping.Amount() != 4500 {
		t.Fatalf("after update (same instance, no wait): %+v", q)
	}
	if q := quote(ctx, t, svc, "EG-GZ", "", 50000); q.Deliverable {
		t.Fatalf("Giza left the zone but is still delivered: %+v", q)
	}
	if err = svc.DeleteZone(ctx, upper.ID); err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Config(ctx)
	if err != nil || len(cfg.Zones) != 1 || cfg.Zones[0].ID != cairo.ID || cfg.Settings.Version != 1 {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	if err = svc.DeleteZone(ctx, cairo.ID); err != nil {
		t.Fatal(err)
	}
	if q := quote(ctx, t, svc, "EG-ALX", "", 50000); !q.Deliverable || !q.Shipping.IsZero() {
		t.Fatalf("no zones left: delivers everywhere again: %+v", q)
	}
	if audits, events := f.rec.counts(); audits != 6 || events != 6 {
		t.Fatalf("audits = %d, events = %d; want 6 each", audits, events)
	}
	if f.rec.actions[0] != "shipping_zone.created" || f.rec.events[0].EventMeta().Type != domain.EventRatesChanged {
		t.Fatalf("first audit %q, event %q", f.rec.actions[0], f.rec.events[0].EventMeta().Type)
	}
}

// TestOtherInstancesFollowInvalidation: another instance serves its
// cached config until it is told, then the new rates.
func TestOtherInstancesFollowInvalidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writer, reader := f.service(), f.service()
	z, err := writer.CreateZone(f.ctxA, domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if q := quote(f.ctxA, t, reader, "EG-C", "", 1000); q.Shipping.Amount() != 5000 {
		t.Fatalf("reader: %+v", q)
	}
	if _, err = writer.UpdateZone(f.ctxA, z.ID, domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: 7000}, 1); err != nil {
		t.Fatal(err)
	}
	f.qc.Reset()
	if q := quote(f.ctxA, t, reader, "EG-C", "", 1000); q.Shipping.Amount() != 5000 || f.qc.Count() != 0 {
		t.Fatalf("reader before invalidation: %+v from %d queries; want the cached 5000 from 0", q, f.qc.Count())
	}
	id, err := tenant.FromContext(f.ctxA)
	if err != nil {
		t.Fatal(err)
	}
	reader.Invalidate(id)
	if q := quote(f.ctxA, t, reader, "EG-C", "", 1000); q.Shipping.Amount() != 7000 {
		t.Fatalf("reader after invalidation: %+v", q)
	}
}

// TestConfigQueryCount: loading a store's config is a fixed number of
// queries however many zones and cities it has (no N+1).
func TestConfigQueryCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	areas := geo.Areas(geo.EG)
	for i := range 10 {
		cities := make([]domain.CityInput, 20)
		for j := range cities {
			cities[j] = domain.CityInput{Name: "مدينة " + strings.Repeat("ب", j+1), RateMinor: int64(j)}
		}
		if _, err := svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "zone", Areas: []string{areas[2*i].Code, areas[2*i+1].Code},
			RateMinor: 1000, Cities: cities}); err != nil {
			t.Fatal(err)
		}
	}
	f.qc.Reset()
	cfg, err := svc.Config(f.ctxA)
	if err != nil {
		t.Fatal(err)
	}
	// BEGIN, set_config, four reads, COMMIT.
	if n := f.qc.Count(); n > 7 || len(cfg.Zones) != 10 || len(cfg.Zones[9].Cities) != 20 || len(cfg.Zones[9].Areas) != 2 {
		t.Fatalf("%d queries for %d zones", n, len(cfg.Zones))
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	z, err := svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: 5000})
	if err != nil {
		t.Fatal(err)
	}
	isField := func(err error, field, code string) bool {
		var e *errs.Error
		if !errors.As(err, &e) || e.Kind != errs.KindValidation {
			return false
		}
		for _, fe := range e.Fields {
			if fe.Field == field && fe.Code == code {
				return true
			}
		}
		return false
	}
	if _, err = svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "y", Areas: []string{"EG-GZ", "EG-C"}}); !isField(err, "areas", "taken") {
		t.Errorf("governorate in two zones: %v", err)
	}
	if _, err = svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "y", Areas: []string{"SA-01"}}); !isField(err, "areas[0]", "invalid") {
		t.Errorf("foreign governorate: %v", err)
	}
	if _, err = svc.UpdateZone(f.ctxA, z.ID, domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}}, 7); !errors.Is(err, app.ErrVersionConflict) {
		t.Errorf("stale zone version: %v", err)
	}
	if _, err = svc.UpdateZone(f.ctxA, f.ids.New(), domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}}, 1); !errors.Is(err, app.ErrZoneNotFound) {
		t.Errorf("missing zone: %v", err)
	}
	if err = svc.DeleteZone(f.ctxA, f.ids.New()); !errors.Is(err, app.ErrZoneNotFound) {
		t.Errorf("delete missing zone: %v", err)
	}
	if _, err = svc.SaveSettings(f.ctxA, domain.Settings{}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SaveSettings(f.ctxA, domain.Settings{}, 0); !errors.Is(err, app.ErrVersionConflict) {
		t.Errorf("second first save: %v", err)
	}
	if _, err = svc.SaveSettings(f.ctxA, domain.Settings{}, 5); !errors.Is(err, app.ErrVersionConflict) {
		t.Errorf("stale settings version: %v", err)
	}
	if _, err = svc.SaveSettings(f.ctxA, domain.Settings{CODFee: domain.CODFee{BP: -1}}, 1); errs.KindOf(err) != errs.KindValidation {
		t.Errorf("invalid settings: %v", err)
	}
	if _, err = svc.Quote(t.Context(), domain.QuoteRequest{Subtotal: egp(t, 1)}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("quote without a store: %v", err)
	}
}

// TestCrossTenant: store B neither sees nor changes store A's zones, and
// a governorate in A's zone is free in B.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	z, err := svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "a", Areas: []string{"EG-C"}, RateMinor: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.UpdateZone(f.ctxB, z.ID, domain.ZoneInput{Name: "b", Areas: []string{"EG-C"}}, 1); !errors.Is(err, app.ErrZoneNotFound) {
		t.Errorf("B updates A's zone: %v", err)
	}
	if err = svc.DeleteZone(f.ctxB, z.ID); !errors.Is(err, app.ErrZoneNotFound) {
		t.Errorf("B deletes A's zone: %v", err)
	}
	if _, err = svc.CreateZone(f.ctxB, domain.ZoneInput{Name: "b", Areas: []string{"EG-C"}, RateMinor: 100}); err != nil {
		t.Fatalf("B uses a governorate A's zone holds: %v", err)
	}
	if cfg, cfgErr := svc.Config(f.ctxB); cfgErr != nil || len(cfg.Zones) != 1 || cfg.Zones[0].Name != "b" {
		t.Fatalf("B's config: %+v %v", cfg, cfgErr)
	}
	if q := quote(f.ctxA, t, svc, "EG-C", "", 1); q.Shipping.Amount() != 5000 {
		t.Fatalf("A's quote changed: %+v", q)
	}
}

// TestConcurrentZonesShareNoGovernorate: two zones racing for one
// governorate: exactly one gets it.
func TestConcurrentZonesShareNoGovernorate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	var wg sync.WaitGroup
	errsCh := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "z", Areas: []string{"EG-ALX"}, RateMinor: 1})
			errsCh <- err
		})
	}
	wg.Wait()
	close(errsCh)
	won := 0
	for err := range errsCh {
		switch {
		case err == nil:
			won++
		case errs.KindOf(err) != errs.KindValidation:
			t.Errorf("loser: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d zones got the governorate", won)
	}
	cfg, err := svc.Config(f.ctxA)
	if err != nil || len(cfg.Zones) != 1 {
		t.Fatalf("zones: %+v %v", cfg, err)
	}
}

func TestDefaultCache(t *testing.T) {
	t.Parallel()
	if c := app.DefaultCacheConfig(); c.TTL > 30*time.Second || c.Stores < 1000 {
		t.Fatalf("cache %+v", c)
	}
}

// TestConfigQueriesUseIndexesUnderRLS: loading a store's config reads
// each table by an index on store_id, as app_user under generic plans,
// among many other stores' zones.
func TestConfigQueriesUseIndexesUnderRLS(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	// 300 other stores with 5 zones, 10 areas and 20 cities each. FORCE
	// RLS binds the owner too, so each store is seeded in its own tenant
	// transaction.
	for n := range 300 {
		ctx := f.mkStore(t)
		store, _ := tenant.FromContext(ctx)
		if err = postgres.InTenantTx(ctx, owner, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `
WITH z AS (
  INSERT INTO shipping_zones (id, store_id, name, rate_minor, position)
  SELECT gen_random_uuid(), $1, 'z', 1000, p FROM generate_series(0, 4) AS p RETURNING id, store_id, position),
a AS (
  INSERT INTO shipping_zone_areas (store_id, zone_id, area)
  SELECT z.store_id, z.id, 'EG-' || chr(65 + z.position) || chr(65 + k) FROM z, generate_series(0, 1) AS k RETURNING 1)
INSERT INTO shipping_city_rates (store_id, zone_id, city_key, city_name, rate_minor)
SELECT z.store_id, z.id, 'city ' || c, 'city ' || c, 500 FROM z, generate_series(1, 4) AS c`, store.ID)
			return execErr
		}); err != nil {
			t.Fatalf("store %d: %v", n, err)
		}
	}
	for _, tbl := range []string{"shipping_zones", "shipping_zone_areas", "shipping_city_rates", "shipping_settings"} {
		if _, err = owner.Exec(t.Context(), "ANALYZE "+tbl); err != nil {
			t.Fatal(err)
		}
	}
	svc := f.service()
	if _, err = svc.CreateZone(f.ctxA, domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: 1,
		Cities: []domain.CityInput{{Name: "المعادي", RateMinor: 1}}}); err != nil {
		t.Fatal(err)
	}
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	captured := app.NewService(app.Deps{DB: pool, IDs: f.ids, Cache: app.DefaultCacheConfig(),
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: money.EGP, Country: geo.EG}, nil
		}})
	if _, err = captured.Config(f.ctxA); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"FROM shipping_settings", "FROM shipping_zones", "FROM shipping_zone_areas", "FROM shipping_city_rates"} {
		plan := testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, marker))
		testkit.AssertIndexCond(t, plan, "store_id")
	}
}
