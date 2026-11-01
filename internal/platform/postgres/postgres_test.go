package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/db/migrations"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func pool(t *testing.T, url string, maxConns int32) *pgxpool.Pool {
	t.Helper()
	p, err := postgres.NewPool(t.Context(), config.DB{
		URL: url, MaxConns: maxConns, MinConns: 0, MaxConnLifetime: time.Minute, StatementTimeout: 5 * time.Second,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrateStatusDownUp(t *testing.T) {
	ctx := t.Context()
	var out bytes.Buffer
	if err := postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateStatus, quiet(), &out); err != nil {
		t.Fatal(err)
	}
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sql") {
			total++
		}
	}
	if strings.Count(out.String(), "applied") != total {
		t.Fatalf("status:\n%s", out.String())
	}
	// Exercise every Down migration, then re-apply.
	for range total {
		if err := postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateDown, quiet(), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if err := postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateUp, quiet(), io.Discard); err != nil {
		t.Fatal(err)
	}
	// Up is idempotent.
	if err := postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateUp, quiet(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateCommand("sideways"), quiet(), io.Discard); err == nil {
		t.Fatal("unknown command must fail")
	}
}

func TestRoles(t *testing.T) {
	t.Parallel()
	app := pool(t, infra.AppURL, 2)
	var super, bypass bool
	if err := app.QueryRow(t.Context(), "SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user").Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatalf("app_user must not bypass RLS (super=%v bypassrls=%v)", super, bypass)
	}
	// River tables are usable by the application role (default privileges).
	if _, err := app.Exec(t.Context(), "SELECT count(*) FROM river_job"); err != nil {
		t.Fatalf("app_user cannot read river_job: %v", err)
	}
}

func TestInTenantTxRequiresTenant(t *testing.T) {
	t.Parallel()
	app := pool(t, infra.AppURL, 2)
	called := false
	err := postgres.InTenantTx(t.Context(), app, func(pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, tenant.ErrNoTenant) || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

// TestRowLevelSecurity proves the second wall from ADR-0002 on a probe
// table that follows the documented policy pattern.
func TestRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	owner := pool(t, infra.OwnerURL, 2)
	if _, err := owner.Exec(ctx, `
		CREATE TABLE rls_probe (store_id uuid NOT NULL, note text NOT NULL);
		CREATE INDEX ON rls_probe (store_id);
		ALTER TABLE rls_probe ENABLE ROW LEVEL SECURITY;
		ALTER TABLE rls_probe FORCE ROW LEVEL SECURITY;
		CREATE POLICY tenant_isolation ON rls_probe
		  USING (store_id = app.current_store_id())
		  WITH CHECK (store_id = app.current_store_id());`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "DROP TABLE rls_probe") })

	// One connection, so every transaction below reuses it: proves the
	// tenant setting does not leak between transactions.
	app := pool(t, infra.AppURL, 1)
	gen := ulid.NewGenerator()
	storeA, storeB := tenant.StoreID{ID: gen.New()}, tenant.StoreID{ID: gen.New()}
	ctxA, ctxB := tenant.WithStore(ctx, storeA), tenant.WithStore(ctx, storeB)

	insert := func(ctx context.Context, store tenant.StoreID, note string) error {
		return postgres.InTenantTx(ctx, app, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO rls_probe (store_id, note) VALUES ($1, $2)", store.ID, note)
			return err
		})
	}
	notes := func(ctx context.Context) []string {
		var out []string
		err := postgres.InTenantTx(ctx, app, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT note FROM rls_probe ORDER BY note")
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if err := insert(ctxA, storeA, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := insert(ctxB, storeB, "b1"); err != nil {
		t.Fatal(err)
	}

	if got := notes(ctxA); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("store A sees %v", got)
	}
	if got := notes(ctxB); len(got) != 1 || got[0] != "b1" {
		t.Fatalf("store B sees %v", got)
	}

	// Store A cannot write a row for store B (WITH CHECK).
	err := insert(ctxA, storeB, "smuggled")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("cross-tenant insert err = %v, want insufficient_privilege (42501)", err)
	}

	// Without a tenant (plain transaction, same pooled connection) nothing is visible.
	var n int
	if err := postgres.InTx(ctx, app, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM rls_probe").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("no-tenant transaction sees %d rows, want 0", n)
	}
}

func TestTxRollback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	owner := pool(t, infra.OwnerURL, 2)
	if _, err := owner.Exec(ctx, "CREATE TABLE tx_probe (v int)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "DROP TABLE tx_probe") })

	boom := errors.New("boom")
	err := postgres.InTx(ctx, owner, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := owner.QueryRow(ctx, "SELECT count(*) FROM tx_probe").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback failed: n=%d err=%v", n, err)
	}
}

// TestReadPool proves ADR-0064's read pool: tenant reads work under RLS,
// and no write gets through, whether its sessions or only its transactions
// are read-only.
func TestReadPool(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	owner := pool(t, infra.OwnerURL, 2)
	if _, err := owner.Exec(ctx, `
		CREATE TABLE read_probe (store_id uuid NOT NULL, note text NOT NULL);
		CREATE INDEX ON read_probe (store_id);
		ALTER TABLE read_probe ENABLE ROW LEVEL SECURITY;
		ALTER TABLE read_probe FORCE ROW LEVEL SECURITY;
		CREATE POLICY tenant_isolation ON read_probe
		  USING (store_id = app.current_store_id())
		  WITH CHECK (store_id = app.current_store_id());`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "DROP TABLE read_probe") })

	gen := ulid.NewGenerator()
	storeA, storeB := tenant.StoreID{ID: gen.New()}, tenant.StoreID{ID: gen.New()}
	ctxA, ctxB := tenant.WithStore(ctx, storeA), tenant.WithStore(ctx, storeB)
	primary := pool(t, infra.AppURL, 2)
	for _, w := range []struct {
		ctx   context.Context
		store tenant.StoreID
	}{{ctxA, storeA}, {ctxB, storeB}} {
		if err := postgres.InTenantTx(w.ctx, primary, func(tx pgx.Tx) error {
			_, err := tx.Exec(w.ctx, "INSERT INTO read_probe (store_id, note) VALUES ($1, 'x')", w.store.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	read, err := postgres.NewReadPool(ctx, config.DB{
		URL: infra.AppURL, MaxConns: 1, MaxConnLifetime: time.Minute, StatementTimeout: 5 * time.Second,
	}, "test-read")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	if err := read.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := postgres.InTenantTx(ctxA, read, func(tx pgx.Tx) error {
		return tx.QueryRow(ctxA, "SELECT count(*) FROM read_probe").Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("store A reads %d rows, %v; want its own 1", n, err)
	}

	wrapped := postgres.ReadOnly(pool(t, infra.AppURL, 1))
	for name, db := range map[string]*postgres.ReadPool{"read pool": read, "wrapped pool": wrapped} {
		err := postgres.InTenantTx(ctxA, db, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctxA, "INSERT INTO read_probe (store_id, note) VALUES ($1, 'w')", storeA.ID)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "25006" {
			t.Fatalf("%s: write err = %v, want read_only_sql_transaction (25006)", name, err)
		}
	}
	// The read pool's sessions are read-only too, not only its transactions.
	var mode string
	if err := postgres.InTx(ctx, read, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SHOW default_transaction_read_only").Scan(&mode)
	}); err != nil || mode != "on" {
		t.Fatalf("session read-only = %q, %v", mode, err)
	}
}

func TestNewPoolBadURL(t *testing.T) {
	t.Parallel()
	_, err := postgres.NewPool(t.Context(), config.DB{URL: "postgres://u:hunter2@host:notaport/db", MaxConns: 1}, "x")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v", err)
	}
}
