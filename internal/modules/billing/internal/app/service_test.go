package app_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/db/migrations"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

type fixture struct {
	svc     *app.Service
	pool    *pgxpool.Pool
	queries *testkit.QueryCounter
	ids     *ulid.Generator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, counter := testkit.CountingPool(t, infra.AppURL)
	return fixture{svc: app.NewService(app.Deps{DB: pool, Now: func() time.Time { return now }}), pool: pool, queries: counter, ids: ulid.NewGenerator()}
}

func (f fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "bill-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// subscribe gives the store in ctx a subscription to the seeded plan
// code, in status, with the remaining columns from extra (SQL).
func (f fixture) subscribe(ctx context.Context, t *testing.T, code, status, extra string) {
	t.Helper()
	cols, vals := "", ""
	if extra != "" {
		k, v, _ := strings.Cut(extra, "=")
		cols, vals = ", "+k, ", "+v
	}
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO subscriptions (store_id, plan_id, status`+cols+`)
			SELECT app.current_store_id(), id, $1`+vals+` FROM plans WHERE code = $2 AND status = 'active'`, status, code)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) grants(ctx context.Context, t *testing.T) domain.Grants {
	t.Helper()
	g, err := f.svc.Grants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGrants(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	late := now.Add(-time.Hour).Format(time.RFC3339)
	soon := now.Add(time.Hour).Format(time.RFC3339)
	tests := []struct {
		name                 string
		code, status, extra  string
		orders, messages     domain.Grant
		adFree, cardPayments domain.Grant
	}{
		{name: "no subscription is the free plan", orders: domain.Grant{Value: 30}, messages: domain.Grant{Value: 0}},
		{
			name: "pro in trial", code: "pro", status: "trialing", extra: "trial_ends_at='" + soon + "'",
			orders: domain.Grant{Value: 1500}, messages: domain.Grant{Value: 2000}, adFree: domain.Grant{Value: 1}, cardPayments: domain.Grant{Value: 1},
		},
		{name: "a trial that ended", code: "pro", status: "trialing", extra: "trial_ends_at='" + late + "'", orders: domain.Grant{Value: 30}},
		{
			name: "brand: unlimited orders", code: "brand", status: "active",
			orders: domain.Grant{Unlimited: true}, messages: domain.Grant{Value: 10000}, adFree: domain.Grant{Value: 1}, cardPayments: domain.Grant{Value: 1},
		},
		{
			name: "past due keeps the plan", code: "merchant", status: "past_due", extra: "grace_until='" + soon + "'",
			orders: domain.Grant{Value: 300}, messages: domain.Grant{Value: 500}, adFree: domain.Grant{Value: 1}, cardPayments: domain.Grant{Value: 1},
		},
		{name: "suspended is the free plan", code: "merchant", status: "suspended", extra: "suspended_at='" + late + "'", orders: domain.Grant{Value: 30}},
		{
			name: "pay as you sell", code: "pays", status: "active",
			orders: domain.Grant{Unlimited: true}, messages: domain.Grant{Value: 300}, adFree: domain.Grant{Value: 1}, cardPayments: domain.Grant{Value: 1},
		},
	}
	for _, tt := range tests {
		ctx := f.store(t)
		if tt.code != "" {
			f.subscribe(ctx, t, tt.code, tt.status, tt.extra)
		}
		g := f.grants(ctx, t)
		for feature, want := range map[string]domain.Grant{
			"orders_per_month": tt.orders, "messages": tt.messages, "ad_free": tt.adFree, "card_payments": tt.cardPayments,
		} {
			if got := g[feature]; got != want {
				t.Errorf("%s: %s = %+v, want %+v", tt.name, feature, got, want)
			}
		}
		if len(g) != 11 {
			t.Errorf("%s: %d grants, want the 11 seeded features", tt.name, len(g))
		}
		// The risk rules (ADR-0066) follow the plan: paid plans only.
		if want := tt.cardPayments.Value; g["risk_rules"].Value != want {
			t.Errorf("%s: risk_rules = %+v, want %d", tt.name, g["risk_rules"], want)
		}
	}
}

func TestGrantsQueries(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	paid := f.store(t)
	f.subscribe(paid, t, "pro", "active", "")
	free := f.store(t)
	for _, tt := range []struct {
		ctx  context.Context
		want int64
	}{{paid, 2}, {free, 3}} {
		f.queries.Reset()
		f.grants(tt.ctx, t)
		// BEGIN, set tenant and COMMIT come with every transaction.
		if got := f.queries.Count() - 3; got != tt.want {
			t.Errorf("queries = %d, want %d", got, tt.want)
		}
	}
}

// TestSubscriptionsAreTenantRows: a store sees its own subscription only,
// cannot delete it, and merchant code cannot change plans.
func TestSubscriptionsAreTenantRows(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.store(t), f.store(t)
	f.subscribe(a, t, "brand", "active", "")
	if g := f.grants(b, t); g["ad_free"] != (domain.Grant{}) {
		t.Errorf("store B got A's plan: %+v", g)
	}
	var n int
	if err := postgres.InTenantTx(b, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(b, `SELECT count(*) FROM subscriptions`).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("store B sees %d subscriptions (%v)", n, err)
	}
	for _, q := range []string{
		`DELETE FROM subscriptions`,
		`UPDATE plans SET trial_days = 90`,
		`UPDATE plan_entitlements SET value = NULL`,
		`INSERT INTO features (key, kind, name_ar, name_en) VALUES ('x', 'flag', 'x', 'x')`,
		`DELETE FROM plan_prices`,
	} {
		err := postgres.InTenantTx(a, f.pool, func(tx pgx.Tx) error { _, err := tx.Exec(a, q); return err })
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: err = %v, want permission denied", q, err)
		}
	}
}

// TestMigrationStartsTrialsForExistingStores re-applies 00056 over a
// store that existed before it: the store gets 14 days of pro. It is not
// parallel, so no other test runs while the tables are gone.
func TestMigrationStartsTrialsForExistingStores(t *testing.T) {
	ctx := t.Context()
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	later := 0
	for _, e := range files {
		if strings.HasSuffix(e.Name(), ".sql") && e.Name() >= "00056" {
			later++
		}
	}
	quiet := slog.New(slog.DiscardHandler)
	for range later {
		if err = postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateDown, quiet, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	f := newFixture(t)
	old := f.store(t)
	if err = postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateUp, quiet, io.Discard); err != nil {
		t.Fatal(err)
	}
	var (
		code, status string
		ends         time.Time
		used         bool
	)
	if err = postgres.InTenantTx(old, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(old, `SELECT p.code, s.status, s.trial_ends_at, s.trial_used FROM subscriptions s JOIN plans p ON p.id = s.plan_id`).
			Scan(&code, &status, &ends, &used)
	}); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(ends); code != "pro" || status != "trialing" || !used || left < 13*24*time.Hour || left > 14*24*time.Hour {
		t.Errorf("existing store: %s %s until %v (used %v)", code, status, ends, used)
	}
	if g, err := app.NewService(app.Deps{DB: f.pool}).Grants(old); err != nil || g["ad_free"].Value != 1 || g["custom_domains"].Value != 5 {
		t.Errorf("trial grants = %+v, %v", g, err)
	}
}
