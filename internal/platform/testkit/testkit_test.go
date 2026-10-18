package testkit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// spy records failures instead of failing the real test.
type spy struct {
	testing.TB
	mu     sync.Mutex
	errors []string
	fatal  bool
}

func (s *spy) Helper() {}

func (s *spy) Errorf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

func (s *spy) Fatalf(format string, args ...any) {
	s.Errorf(format, args...)
	s.fatal = true
	runtimeGoexit()
}

func (s *spy) Fatal(args ...any) { s.Fatalf("%s", fmt.Sprint(args...)) }

// run calls fn with a spy on its own goroutine, so Fatal can stop it.
func run(t *testing.T, fn func(tb testing.TB)) *spy {
	t.Helper()
	s := &spy{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(s)
	}()
	<-done
	return s
}

func problem404(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"title":"store not found","status":404}`))
}

func TestAssertCrossTenantDenied(t *testing.T) {
	t.Parallel()
	get := func() *http.Request {
		return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/stores/B/domains", nil)
	}
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		c         testkit.CrossTenantCase
		wantFails []string // substrings of expected failures; empty = pass
	}{
		{
			name:    "proper 404 passes",
			handler: problem404,
			c:       testkit.CrossTenantCase{OperationID: "listDomains", Request: get(), Leaks: []string{"shop-b.example"}},
		},
		{
			name: "200 with data fails",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Store", "shop-b.example")
				_, _ = w.Write([]byte(`[{"host":"shop-b.example"}]`))
			},
			c:         testkit.CrossTenantCase{OperationID: "listDomains", Request: get(), Leaks: []string{"shop-b.example"}},
			wantFails: []string{"status = 200", "content type", "leaks"},
		},
		{
			name: "403 reveals existence and fails",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusForbidden)
			},
			c:         testkit.CrossTenantCase{OperationID: "listDomains", Request: get()},
			wantFails: []string{"status = 403"},
		},
		{
			name:      "write without Unchanged is rejected",
			handler:   problem404,
			c:         testkit.CrossTenantCase{OperationID: "deleteDomain", Request: httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/stores/B/domains/1", nil)},
			wantFails: []string{"set Unchanged"},
		},
		{
			name:      "missing operation id is rejected",
			handler:   problem404,
			c:         testkit.CrossTenantCase{Request: get()},
			wantFails: []string{"needs OperationID"},
		},
		{
			name:    "Unchanged runs and can fail",
			handler: problem404,
			c: testkit.CrossTenantCase{
				OperationID: "deleteDomain",
				Request:     httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/stores/B/domains/1", nil),
				Unchanged:   func(tb testing.TB) { tb.Errorf("store B's domain was deleted") },
			},
			wantFails: []string{"was deleted"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var reg testkit.Registry
			s := run(t, func(tb testing.TB) { testkit.AssertCrossTenantDenied(tb, &reg, tt.handler, tt.c) })
			got := strings.Join(s.errors, "\n")
			if len(tt.wantFails) == 0 && got != "" {
				t.Fatalf("unexpected failures:\n%s", got)
			}
			for _, want := range tt.wantFails {
				if !strings.Contains(got, want) {
					t.Fatalf("missing failure %q in:\n%s", want, got)
				}
			}
		})
	}
}

func TestRegistryAndStoreScopedOperations(t *testing.T) {
	t.Parallel()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /v1/stores/{store_id}:
    get: {operationId: getStore, tags: [tenancy], responses: {"200": {description: ok}}}
  /v1/stores/{store_id}/members:
    get: {operationId: listMembers, tags: [identity], responses: {"200": {description: ok}}}
    post: {operationId: inviteMember, tags: [identity], responses: {"200": {description: ok}}}
  /v1/me:
    get: {operationId: getMe, tags: [identity], responses: {"200": {description: ok}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	all := testkit.StoreScopedOperations(spec)
	if strings.Join(all, ",") != "getStore,inviteMember,listMembers" {
		t.Fatalf("all = %v", all)
	}
	identity := testkit.StoreScopedOperations(spec, "identity")
	if strings.Join(identity, ",") != "inviteMember,listMembers" {
		t.Fatalf("identity = %v", identity)
	}

	var reg testkit.Registry
	if m := reg.Missing(identity); strings.Join(m, ",") != "inviteMember,listMembers" {
		t.Fatalf("missing on empty registry = %v", m)
	}
	s := run(t, func(tb testing.TB) {
		testkit.AssertCrossTenantDenied(tb, &reg, http.HandlerFunc(problem404), testkit.CrossTenantCase{
			OperationID: "listMembers", Request: httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
		})
	})
	if len(s.errors) != 0 {
		t.Fatal(s.errors)
	}
	if m := reg.Missing(identity); strings.Join(m, ",") != "inviteMember" {
		t.Fatalf("missing = %v", m)
	}
}

func ownerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestRLSProblems builds good and broken tenant tables inside a rolled-back
// transaction and checks each broken one is reported.
func TestRLSProblems(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := ownerPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const policy = `CREATE POLICY tenant_isolation ON %[1]s
		USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())`
	ddl := []string{
		`CREATE TABLE tk_good (store_id uuid NOT NULL)`,
		`ALTER TABLE tk_good ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_good FORCE ROW LEVEL SECURITY`,
		fmt.Sprintf(policy, "tk_good"),

		`CREATE TABLE tk_no_rls (store_id uuid NOT NULL)`,

		`CREATE TABLE tk_not_forced (store_id uuid NOT NULL)`,
		`ALTER TABLE tk_not_forced ENABLE ROW LEVEL SECURITY`, fmt.Sprintf(policy, "tk_not_forced"),

		`CREATE TABLE tk_nullable (store_id uuid)`,
		`ALTER TABLE tk_nullable ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_nullable FORCE ROW LEVEL SECURITY`,
		fmt.Sprintf(policy, "tk_nullable"),

		`CREATE TABLE tk_no_policy (store_id uuid NOT NULL)`,
		`ALTER TABLE tk_no_policy ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_no_policy FORCE ROW LEVEL SECURITY`,

		`CREATE TABLE tk_select_only (store_id uuid NOT NULL)`,
		`ALTER TABLE tk_select_only ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_select_only FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY tenant_isolation ON tk_select_only FOR SELECT USING (store_id = app.current_store_id())`,

		`CREATE TABLE tk_extra (store_id uuid NOT NULL, user_id uuid)`,
		`ALTER TABLE tk_extra ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_extra FORCE ROW LEVEL SECURITY`,
		fmt.Sprintf(policy, "tk_extra"),
		`CREATE POLICY wide_open ON tk_extra FOR SELECT TO app_user USING (true)`,
		`CREATE POLICY owner_reads ON tk_extra FOR SELECT TO app_owner USING (true)`,
		`CREATE POLICY narrowing ON tk_extra AS RESTRICTIVE USING (true)`,

		`CREATE TABLE tk_allowed (store_id uuid NOT NULL, user_id uuid)`,
		`ALTER TABLE tk_allowed ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_allowed FORCE ROW LEVEL SECURITY`,
		fmt.Sprintf(policy, "tk_allowed"),
		`CREATE POLICY self_read ON tk_allowed FOR SELECT USING (user_id IS NOT NULL)`,

		`CREATE TABLE tk_parted (store_id uuid NOT NULL, at date NOT NULL) PARTITION BY RANGE (at)`,
		`ALTER TABLE tk_parted ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_parted FORCE ROW LEVEL SECURITY`,
		fmt.Sprintf(policy, "tk_parted"),
		`CREATE TABLE tk_parted_2026 PARTITION OF tk_parted FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,
	}
	for _, q := range ddl {
		if _, execErr := tx.Exec(ctx, q); execErr != nil {
			t.Fatalf("%s: %v", q, execErr)
		}
	}

	problems, err := testkit.RLSProblems(ctx, tx, "tk_allowed.self_read")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range problems {
		table, _, _ := strings.Cut(p, ":")
		got[table] = p
	}
	want := map[string]string{
		"tk_no_rls":          "not enabled",
		"tk_not_forced":      "not forced",
		"tk_nullable":        "nullable",
		"tk_no_policy":       "policy missing",
		"tk_select_only":     "policy missing",
		"tk_extra.wide_open": "extra permissive",
		"tk_parted_2026":     "not enabled", // partitions need their own RLS
	}
	for table, frag := range want {
		if !strings.Contains(got[table], frag) {
			t.Errorf("%s: got %q, want it to mention %q", table, got[table], frag)
		}
	}
	for _, clean := range []string{"tk_good", "tk_allowed", "tk_parted", "tk_extra"} {
		if p, ok := got[clean]; ok {
			t.Errorf("%s must be clean, got %q", clean, p)
		}
	}
	if p, ok := got["tk_allowed.self_read"]; ok {
		t.Errorf("allowed policy reported: %q", p)
	}

	tables, err := testkit.TenantTables(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(tables, ","), "tk_parted_2026") {
		t.Fatalf("tables = %v", tables)
	}
}

// TestNoRowsWithoutTenantAndCounter seeds a real RLS table and checks the
// raw unfiltered query sees nothing as app_user, and that queries are
// counted.
func TestNoRowsWithoutTenantAndCounter(t *testing.T) {
	ctx := t.Context()
	owner := ownerPool(t)
	ddl := []string{
		`CREATE TABLE tk_seeded (store_id uuid NOT NULL, v text)`,
		`ALTER TABLE tk_seeded ENABLE ROW LEVEL SECURITY`, `ALTER TABLE tk_seeded FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY tenant_isolation ON tk_seeded USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())`,
	}
	for _, q := range ddl {
		if _, err := owner.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = owner.Exec(ctx, `DROP TABLE tk_seeded`) })

	for _, store := range []string{"0190a0b0-0000-7000-8000-00000000000a", "0190a0b0-0000-7000-8000-00000000000b"} {
		err := pgx.BeginFunc(ctx, owner, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.store_id', $1, true)`, store); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO tk_seeded (store_id, v) VALUES ($1, 'secret')`, store)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	app, qc := testkit.CountingPool(t, infra.AppURL)
	testkit.AssertNoRowsWithoutTenant(t, app)
	if qc.Count() < 2 {
		t.Fatalf("query count = %d, want >= 2 (catalog + table)", qc.Count())
	}
	qc.Reset()
	if qc.Count() != 0 {
		t.Fatal("reset")
	}

	// The same assertion catches a table that leaks.
	if _, err := owner.Exec(ctx, `ALTER TABLE tk_seeded NO FORCE ROW LEVEL SECURITY`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `CREATE POLICY wide_open ON tk_seeded FOR SELECT TO app_user USING (true)`); err != nil {
		t.Fatal(err)
	}
	s := run(t, func(tb testing.TB) { testkit.AssertNoRowsWithoutTenant(tb, app) })
	if !strings.Contains(strings.Join(s.errors, "\n"), "tk_seeded: 2 rows visible") {
		t.Fatalf("leak not detected: %v", s.errors)
	}
}

func runtimeGoexit() { runtime.Goexit() }
