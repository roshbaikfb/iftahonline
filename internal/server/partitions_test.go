package server_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/server"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// TestPartitionsWorker: the platform job keeps every registered table
// partitioned ahead (here audit_log and a table registered by the test,
// the way a module's migration registers one), runs as app_user, is
// idempotent, and every partition passes the RLS catalog check. Mutable
// tables keep UPDATE on their partitions; append-only ones do not.
func TestPartitionsWorker(t *testing.T) {
	ctx := t.Context()
	owner, err := pgxpool.New(ctx, infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	app, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)

	for _, q := range []string{
		`CREATE TABLE things (id uuid NOT NULL, store_id uuid NOT NULL REFERENCES stores (id),
		   created_at timestamptz NOT NULL, PRIMARY KEY (store_id, created_at, id)) PARTITION BY RANGE (created_at)`,
		`ALTER TABLE things ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE things FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY tenant_isolation ON things USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())`,
		`INSERT INTO app.monthly_partitioned (parent, key_column, append_only) VALUES ('things', 'created_at', false)`,
	} {
		if _, err = owner.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	w := &server.PartitionsWorker{DB: app}
	for range 2 {
		if err = w.Work(ctx, &river.Job[server.PartitionsArgs]{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, parent := range []string{"audit_log", "things"} {
		var n int
		if err = owner.QueryRow(ctx, "SELECT count(*) FROM pg_inherits WHERE inhparent = $1::regclass", parent).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < server.PartitionsMonthsAhead+1 {
			t.Errorf("%s partitions = %d, want >= %d", parent, n, server.PartitionsMonthsAhead+1)
		}
	}
	var thingsUpdate, auditUpdate bool
	if err = owner.QueryRow(ctx, `SELECT
		  has_table_privilege('app_user', format('things_%s', to_char(now(), 'YYYY_MM')), 'UPDATE'),
		  has_table_privilege('app_user', format('audit_log_%s', to_char(now(), 'YYYY_MM')), 'UPDATE')`).
		Scan(&thingsUpdate, &auditUpdate); err != nil {
		t.Fatal(err)
	}
	if !thingsUpdate || auditUpdate {
		t.Errorf("UPDATE on partitions: things=%v (want true), audit_log=%v (want false)", thingsUpdate, auditUpdate)
	}
	testkit.AssertTenantTablesUseRLS(t, owner, "store_memberships.member_self_read")
	if server.PartitionsPeriodic() == nil {
		t.Fatal("periodic job")
	}
}
