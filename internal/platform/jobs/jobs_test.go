package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type tenantJob struct {
	jobs.TenantArgs
	Note string `json:"note"`
}

func (tenantJob) Kind() string { return "test.tenant" }

type globalJob struct {
	jobs.GlobalArgs
}

func (globalJob) Kind() string { return "test.global" }

type unscopedJob struct{}

func (unscopedJob) Kind() string { return "test.unscoped" }

type bothJob struct {
	jobs.TenantArgs
	jobs.GlobalArgs
}

func (bothJob) Kind() string { return "test.both" }

func newStore() tenant.StoreID { return tenant.StoreID{ID: ulid.NewGenerator().New()} }

func insertVia(ctx context.Context, args river.JobArgs) error {
	var mw interface {
		InsertMany(context.Context, []*rivertype.JobInsertParams, func(context.Context) ([]*rivertype.JobInsertResult, error)) ([]*rivertype.JobInsertResult, error)
	}
	for _, m := range jobs.Middleware() {
		if im, ok := m.(rivertype.JobInsertMiddleware); ok {
			mw = im
		}
	}
	_, err := mw.InsertMany(ctx, []*rivertype.JobInsertParams{{Args: args, Kind: args.Kind()}},
		func(context.Context) ([]*rivertype.JobInsertResult, error) { return nil, nil })
	return err
}

func TestInsertMiddleware(t *testing.T) {
	t.Parallel()
	a, b := newStore(), newStore()
	inA := tenant.WithStore(t.Context(), a)
	tests := []struct {
		name    string
		ctx     context.Context
		args    river.JobArgs
		wantErr error
	}{
		{"tenant job, same store", inA, tenantJob{TenantArgs: jobs.TenantArgs{StoreID: a}}, nil},
		{"tenant job, other store", inA, tenantJob{TenantArgs: jobs.TenantArgs{StoreID: b}}, jobs.ErrTenantMismatch},
		{"tenant job, no tenant in ctx", t.Context(), tenantJob{TenantArgs: jobs.TenantArgs{StoreID: a}}, tenant.ErrNoTenant},
		{"tenant job, zero store", inA, tenantJob{}, tenant.ErrNoTenant},
		{"global job without tenant", t.Context(), globalJob{}, nil},
		{"global job inside a tenant", inA, globalJob{}, nil},
		{"unscoped job", inA, unscopedJob{}, jobs.ErrNoScope},
		{"both scopes", inA, bothJob{TenantArgs: jobs.TenantArgs{StoreID: a}}, jobs.ErrNoScope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := insertVia(tt.ctx, tt.args)
			if tt.wantErr == nil && err != nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestForTenant(t *testing.T) {
	t.Parallel()
	a := newStore()
	got, err := jobs.ForTenant(tenant.WithStore(t.Context(), a))
	if err != nil || got.StoreID != a {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := jobs.ForTenant(t.Context()); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreTenant(t *testing.T) {
	t.Parallel()
	a, b := newStore(), newStore()
	inB := tenant.WithStore(t.Context(), b)
	tests := []struct {
		name    string
		args    string
		want    tenant.StoreID // zero: no tenant
		wantErr bool
	}{
		{"tenant", `{"store_id":"` + a.String() + `","note":"x"}`, a, false},
		{"global clears inherited tenant", `{}`, tenant.StoreID{}, false},
		{"null store is global", `{"store_id":null}`, tenant.StoreID{}, false},
		{"zero store", `{"store_id":"00000000000000000000000000"}`, tenant.StoreID{}, true},
		{"malformed store", `{"store_id":"nope"}`, tenant.StoreID{}, true},
		{"store is a number", `{"store_id":7}`, tenant.StoreID{}, true},
		{"not json", `{`, tenant.StoreID{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, err := jobs.RestoreTenant(inB, []byte(tt.args))
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := tenant.FromContext(ctx)
			if tt.want.IsZero() {
				if !errors.Is(err, tenant.ErrNoTenant) {
					t.Fatalf("want no tenant, got %v (%v)", got, err)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("tenant = %v, want %v", got, tt.want)
			}
		})
	}
}

func FuzzRestoreTenant(f *testing.F) {
	f.Add([]byte(`{"store_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"store_id":null}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		ctx, err := jobs.RestoreTenant(t.Context(), b)
		if err != nil {
			return
		}
		// Success means either no tenant or a non-zero one; never a zero store.
		if id, err := tenant.FromContext(ctx); err == nil && id.IsZero() {
			t.Fatal("restored a zero store")
		}
	})
}

// captureWorker records the tenant each job saw.
type captureWorker struct {
	river.WorkerDefaults[tenantJob]
	seen chan tenant.StoreID
}

func (w *captureWorker) Work(ctx context.Context, _ *river.Job[tenantJob]) error {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	w.seen <- id
	return nil
}

// TestWorkerRestoresTenant runs a real River client on PostgreSQL: a job
// inserted in store A's transaction is worked with store A in context.
func TestWorkerRestoresTenant(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	w := &captureWorker{seen: make(chan tenant.StoreID, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, w)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Queues:     map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
		Workers:    workers,
		Middleware: jobs.Middleware(),
		TestOnly:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	a, b := newStore(), newStore()
	inA := tenant.WithStore(ctx, a)

	// Enqueueing for another store fails and leaves nothing behind.
	err = pgx.BeginFunc(inA, pool, func(tx pgx.Tx) error {
		_, insErr := client.InsertTx(inA, tx, tenantJob{TenantArgs: jobs.TenantArgs{StoreID: b}}, nil)
		return insErr
	})
	if !errors.Is(err, jobs.ErrTenantMismatch) {
		t.Fatalf("cross-tenant insert: err = %v", err)
	}

	err = pgx.BeginFunc(inA, pool, func(tx pgx.Tx) error {
		args, argsErr := jobs.ForTenant(inA)
		if argsErr != nil {
			return argsErr
		}
		_, insErr := client.InsertTx(inA, tx, tenantJob{TenantArgs: args, Note: "hello"}, nil)
		return insErr
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-w.seen:
		if got != a {
			t.Fatalf("worker saw tenant %v, want %v", got, a)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("job was not worked")
	}

	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'test.tenant'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("river_job rows = %d, want 1 (the rejected insert must leave nothing)", n)
	}
}
