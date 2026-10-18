package testkit

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryCounter is a pgx tracer that counts queries. Tests use it to prove
// a code path does N queries (no N+1) or none at all (cache hits).
type QueryCounter struct{ n atomic.Int64 }

// TraceQueryStart implements pgx.QueryTracer.
func (c *QueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (*QueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Count returns the number of queries so far.
func (c *QueryCounter) Count() int64 { return c.n.Load() }

// Reset sets the count to zero.
func (c *QueryCounter) Reset() { c.n.Store(0) }

// CountingPool opens a pool on url whose queries are counted. It is closed
// when the test ends.
func CountingPool(t testing.TB, url string) (*pgxpool.Pool, *QueryCounter) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	qc := &QueryCounter{}
	cfg.ConnConfig.Tracer = qc
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, qc
}

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// TenantTables returns every table (partitioned tables and their
// partitions included) in the public schema with a store_id column.
func TenantTables(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'store_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("testkit: tenant tables: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("testkit: tenant tables: %w", err)
	}
	return names, nil
}

// RLSProblems lists every tenant table that is not fully behind the second
// wall (ADR-0002): RLS enabled and forced, store_id NOT NULL, and a
// tenant_isolation policy whose USING and WITH CHECK both compare store_id
// with app.current_store_id(). Partitions are checked too, because
// querying a partition directly skips its parent's policies.
//
// Permissive policies are OR-ed, so any other permissive policy that
// applies to app_user (or PUBLIC) widens the wall and is a problem unless
// listed in allowed as "table.policy" (e.g. a member's read of their own
// memberships). Policies for other roles and restrictive policies are
// fine: they cannot widen what app_user sees.
func RLSProblems(ctx context.Context, db Querier, allowed ...string) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT c.relname,
		       c.relrowsecurity,
		       c.relforcerowsecurity,
		       a.attnotnull,
		       coalesce(pg_get_expr(p.polqual, p.polrelid), ''),
		       coalesce(pg_get_expr(p.polwithcheck, p.polrelid), ''),
		       coalesce(p.polcmd = '*', false)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'store_id' AND NOT a.attisdropped
		LEFT JOIN pg_policy p ON p.polrelid = c.oid AND p.polname = 'tenant_isolation'
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("testkit: rls catalog: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var (
			name                string
			enabled, forced, nn bool
			using, check        string
			allCommands         bool
		)
		if scanErr := rows.Scan(&name, &enabled, &forced, &nn, &using, &check, &allCommands); scanErr != nil {
			return nil, fmt.Errorf("testkit: rls catalog: %w", scanErr)
		}
		const want = "(store_id = app.current_store_id())"
		switch {
		case !enabled:
			problems = append(problems, name+": row level security is not enabled")
		case !forced:
			problems = append(problems, name+": row level security is not forced")
		case !nn:
			problems = append(problems, name+": store_id is nullable")
		case using != want || check != want:
			problems = append(problems, fmt.Sprintf("%s: tenant_isolation policy missing or wrong (USING %q, WITH CHECK %q)", name, using, check))
		case !allCommands:
			problems = append(problems, name+": tenant_isolation must apply to ALL commands")
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("testkit: rls catalog: %w", err)
	}
	extra, err := extraPolicies(ctx, db)
	if err != nil {
		return nil, err
	}
	for _, e := range extra {
		if !slices.Contains(allowed, e) {
			problems = append(problems, e+": extra permissive policy applies to app_user")
		}
	}
	return problems, nil
}

func extraPolicies(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT c.relname || '.' || p.polname
		FROM pg_policy p
		JOIN pg_class c ON c.oid = p.polrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND p.polpermissive
		  AND p.polname <> 'tenant_isolation'
		  AND EXISTS (SELECT 1 FROM pg_attribute a
		              WHERE a.attrelid = c.oid AND a.attname = 'store_id' AND NOT a.attisdropped)
		  AND (0::oid = ANY (p.polroles)
		       OR (SELECT oid FROM pg_roles WHERE rolname = 'app_user') = ANY (p.polroles))
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("testkit: rls policies: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("testkit: rls policies: %w", err)
	}
	return names, nil
}

// AssertTenantTablesUseRLS fails the test for every RLSProblems entry.
func AssertTenantTablesUseRLS(t testing.TB, db Querier, allowed ...string) {
	t.Helper()
	problems, err := RLSProblems(t.Context(), db, allowed...)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// AssertNoRowsWithoutTenant runs, as the given (app_user) pool and outside
// any tenant transaction, a raw SELECT on every tenant table with no
// store_id filter, and asserts each returns 0 rows. Call it after seeding
// data for at least two stores.
func AssertNoRowsWithoutTenant(t testing.TB, app *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	tables, err := TenantTables(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		var n int64
		// Identifiers come from pg_catalog, not input, and are quoted.
		q := "SELECT count(*) FROM " + pgx.Identifier{"public", table}.Sanitize()
		if err := app.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows visible without a tenant, want 0", table, n)
		}
	}
}
