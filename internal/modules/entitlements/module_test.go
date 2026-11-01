package entitlements_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func connect(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newStore creates a store, on the plan with this code when it is not
// empty (as the console will, P7 7e).
func newStore(t *testing.T, pool *pgxpool.Pool, code string) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: ulid.NewGenerator().New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "entm-"+strings.ToLower(id.String()[14:]))
		if err != nil || code == "" {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO subscriptions (store_id, plan_id, status)
			SELECT app.current_store_id(), id, 'active' FROM plans WHERE code = $1 AND status = 'active'`, code)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// TestGrantsFromPlans checks entitlements against the seeded plans
// through the billing module, as wired in cmd/platform.
func TestGrantsFromPlans(t *testing.T) {
	t.Parallel()
	pool := connect(t, infra.AppURL)
	bill, err := billing.New(billing.Deps{Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	mod, err := entitlements.New(entitlements.Deps{Pool: pool, Source: entitlements.FromBilling(bill.Grants())})
	if err != nil {
		t.Fatal(err)
	}
	ent := mod.Entitlements()
	free, brand := newStore(t, pool, ""), newStore(t, pool, "brand")

	if ok, aErr := ent.Allowed(free, entitlements.AdFree); aErr != nil || ok {
		t.Errorf("free store ad-free = %v, %v", ok, aErr)
	}
	if ok, aErr := ent.Allowed(brand, entitlements.AdFree); aErr != nil || !ok {
		t.Errorf("brand store ad-free = %v, %v", ok, aErr)
	}
	// The risk rules (ADR-0066): every paid plan, not the free one.
	if ok, aErr := ent.Allowed(free, entitlements.RiskRules); aErr != nil || ok {
		t.Errorf("free store risk rules = %v, %v", ok, aErr)
	}
	if ok, aErr := ent.Allowed(brand, entitlements.RiskRules); aErr != nil || !ok {
		t.Errorf("brand store risk rules = %v, %v", ok, aErr)
	}
	admit := func(ctx context.Context, have int64) (g entitlements.Grant, err error) {
		err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) (txErr error) {
			g, txErr = ent.AdmitTx(ctx, tx, entitlements.Products, 1, func(context.Context, pgx.Tx) (int64, error) { return have, nil })
			return txErr
		})
		return g, err
	}
	if _, err = admit(free, 50); !errors.Is(err, entitlements.ErrLimitReached) {
		t.Errorf("free store's 51st product: %v", err)
	}
	if g, aErr := admit(brand, 1_000_000); aErr != nil || !g.Unlimited {
		t.Errorf("brand products = %+v, %v", g, aErr)
	}
	if u, err := ent.Usage(free, entitlements.Messages); err != nil || u.Limit != 0 {
		t.Errorf("free store messages = %+v, %v", u, err)
	}
	if u, err := ent.Usage(brand, entitlements.OrdersPerMonth); err != nil || !u.Unlimited() {
		t.Errorf("brand orders = %+v, %v", u, err)
	}
}

// TestDisplayOnlyFeatures: a feature the console adds to the catalog
// and to a plan is shown, not enforced: entitlements leaves it out.
func TestDisplayOnlyFeatures(t *testing.T) {
	t.Parallel()
	owner, pool := connect(t, infra.OwnerURL), connect(t, infra.AppURL)
	if _, err := owner.Exec(t.Context(), `
		INSERT INTO features (key, kind, name_ar, name_en) VALUES ('loyalty_points', 'flag', 'نقاط الولاء', 'Loyalty points')
		ON CONFLICT DO NOTHING;
		INSERT INTO plan_entitlements (plan_id, feature, value)
		SELECT id, 'loyalty_points', 1 FROM plans WHERE code = 'pro' ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	bill, err := billing.New(billing.Deps{Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	ctx := newStore(t, pool, "pro")
	var g entitlements.Grants
	err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) (txErr error) {
		g, txErr = entitlements.FromBilling(bill.Grants()).GrantsTx(ctx, tx)
		return txErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g["loyalty_points"]; ok || len(g) != 11 || g[entitlements.AdFree].Value != 1 || g[entitlements.RiskRules].Value != 1 {
		t.Errorf("grants = %+v", g)
	}
}

func TestNewNeedsItsDependencies(t *testing.T) {
	t.Parallel()
	if _, err := entitlements.New(entitlements.Deps{Source: entitlements.Fixed{}}); err == nil {
		t.Error("no pool: want error")
	}
	if _, err := entitlements.New(entitlements.Deps{Pool: connect(t, infra.AppURL)}); err == nil {
		t.Error("no source: want error")
	}
}

// countingGrants counts its reads.
type countingGrants struct{ reads atomic.Int64 }

func (c *countingGrants) GrantsTx(context.Context, pgx.Tx) (entitlements.Grants, error) {
	c.reads.Add(1)
	return entitlements.Grants{entitlements.AdFree: {Value: 1}}, nil
}

// TestInvalidationOverRedis: a plan change on one instance drops every
// instance's cached grants of that store, through Redis (ADR-0050).
func TestInvalidationOverRedis(t *testing.T) {
	t.Parallel()
	pool := connect(t, infra.AppURL)
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	src := &countingGrants{}
	mod, err := entitlements.New(entitlements.Deps{Pool: pool, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go mod.Listen(ctx, rdb, slog.New(slog.DiscardHandler))
	store := newStore(t, pool, "")
	id, _ := tenant.FromContext(store)
	check := func() {
		t.Helper()
		if _, aErr := mod.Entitlements().Allowed(store, entitlements.AdFree); aErr != nil {
			t.Fatal(aErr)
		}
	}
	check()
	check()
	if src.reads.Load() != 1 {
		t.Fatalf("%d reads before, want 1 (cached)", src.reads.Load())
	}
	handler := entitlements.InvalidateHandler(rdb)
	deadline := time.Now().Add(5 * time.Second)
	for src.reads.Load() < 2 {
		// The subscription may not be up yet: publish until it is heard.
		if err = handler(t.Context(), eventbus.Delivery{Meta: event.Meta{StoreID: id}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		check()
		if time.Now().After(deadline) {
			t.Fatal("the invalidation never arrived")
		}
	}
}
