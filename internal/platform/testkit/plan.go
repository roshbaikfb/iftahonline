package testkit

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// SQLCapture is a pgx tracer that keeps every query's SQL and arguments,
// so a test can EXPLAIN exactly what the code sent.
type SQLCapture struct {
	mu      sync.Mutex
	queries []pgx.TraceQueryStartData
}

// TraceQueryStart implements pgx.QueryTracer.
func (c *SQLCapture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, d)
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (*SQLCapture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Last returns the last captured query whose SQL contains substr.
func (c *SQLCapture) Last(t testing.TB, substr string) pgx.TraceQueryStartData {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.queries) - 1; i >= 0; i-- {
		if strings.Contains(c.queries[i].SQL, substr) {
			return c.queries[i]
		}
	}
	t.Fatalf("testkit: no captured query contains %q", substr)
	return pgx.TraceQueryStartData{}
}

// Count is how many captured queries' SQL contains substr: a request's
// cost in queries.
func (c *SQLCapture) Count(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, q := range c.queries {
		if strings.Contains(q.SQL, substr) {
			n++
		}
	}
	return n
}

// CapturingPool opens a pool on url whose queries are captured. It is
// closed when the test ends.
func CapturingPool(t testing.TB, url string) (*pgxpool.Pool, *SQLCapture) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	c := &SQLCapture{}
	cfg.ConnConfig.Tracer = c
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, c
}

// GenericPlan EXPLAINs q in a tenant transaction of the store in ctx, as
// the pool's role (app_user, so RLS applies) and with the generic plan a
// prepared statement may switch to: the plan that must hold for every
// store, small or large. Sequential scans are disabled so a plan that
// could use an index shows it; one that cannot still falls back to a
// scan, which the caller's assertion catches.
func GenericPlan(ctx context.Context, t testing.TB, db postgres.Beginner, q pgx.TraceQueryStartData) string {
	t.Helper()
	var plan strings.Builder
	err := postgres.InTenantTx(ctx, db, func(tx pgx.Tx) error {
		for _, set := range []string{"SET LOCAL plan_cache_mode = force_generic_plan", "SET LOCAL enable_seqscan = off"} {
			if _, err := tx.Exec(ctx, set); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+q.SQL, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				return err
			}
			plan.WriteString(line + "\n")
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan.String()
}

// AssertIndexCond fails unless plan has an "Index Cond" line naming
// column: the lookup is an index search, not a filter over the store.
func AssertIndexCond(t testing.TB, plan, column string) {
	t.Helper()
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "Index Cond:") && strings.Contains(line, column) {
			return
		}
	}
	t.Fatalf("%s is not an index condition:\n%s", column, plan)
}
