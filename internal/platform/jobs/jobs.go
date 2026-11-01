// Package jobs carries the tenant through River jobs (ADR-0003, ADR-0007).
//
// Every job's args declare their scope by embedding exactly one of:
//
//   - TenantArgs: the job acts on one store. Its store_id is checked at
//     insert time against tenant.FromContext, and restored into the
//     worker's context with tenant.WithStore before the job runs, so
//     postgres.InTenantTx and RLS apply as they do in HTTP handlers.
//   - GlobalArgs: the job is not about one store (a user's new-device
//     alert, partition maintenance). Its context carries no tenant, so any
//     tenant-scoped code it reaches fails with tenant.ErrNoTenant.
//
// Args that declare neither are rejected at insert time.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Errors returned by the insert middleware.
var (
	ErrNoScope        = errors.New("jobs: args must embed jobs.TenantArgs or jobs.GlobalArgs")
	ErrTenantMismatch = errors.New("jobs: job store_id differs from the tenant in context")
)

// Unique is the insert options of a job enqueued at most once for the
// same args while one is pending (a fan-out that may run twice).
func Unique() *river.InsertOpts { return &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}} }

// TenantArgs scopes a job to one store. Embed it in the job's args and set
// it from tenant.FromContext.
type TenantArgs struct {
	StoreID tenant.StoreID `json:"store_id"`
}

// JobStoreID returns the job's store.
func (a TenantArgs) JobStoreID() tenant.StoreID { return a.StoreID }

// ForTenant returns TenantArgs for the store in ctx.
func ForTenant(ctx context.Context) (TenantArgs, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return TenantArgs{}, fmt.Errorf("jobs: %w", err)
	}
	return TenantArgs{StoreID: id}, nil
}

// GlobalArgs marks a job that is not scoped to a store. Embed it in the
// job's args.
type GlobalArgs struct{}

// JobGlobal marks the args as global.
func (GlobalArgs) JobGlobal() {}

type tenantScoped interface{ JobStoreID() tenant.StoreID }

type globalScoped interface{ JobGlobal() }

// Middleware returns the insert and worker middleware, in order, for
// river.Config.Middleware.
func Middleware() []rivertype.Middleware {
	return []rivertype.Middleware{&insertMiddleware{}, &workerMiddleware{}}
}

type insertMiddleware struct{ river.MiddlewareDefaults }

// InsertMany rejects args without a scope, tenant args with a zero store,
// and tenant args for a store other than the tenant in ctx. A job can
// therefore only be enqueued for the store whose request or job is
// running, never for one named by input.
func (*insertMiddleware) InsertMany(ctx context.Context, many []*rivertype.JobInsertParams, doInner func(context.Context) ([]*rivertype.JobInsertResult, error)) ([]*rivertype.JobInsertResult, error) {
	for _, p := range many {
		if err := checkInsert(ctx, p.Args); err != nil {
			return nil, fmt.Errorf("%w (kind %s)", err, p.Kind)
		}
	}
	return doInner(ctx)
}

func checkInsert(ctx context.Context, args river.JobArgs) error {
	switch a := args.(type) {
	case tenantScoped:
		if _, global := args.(globalScoped); global {
			return ErrNoScope
		}
		id := a.JobStoreID()
		if id.IsZero() {
			return tenant.ErrNoTenant
		}
		cur, err := tenant.FromContext(ctx)
		if err != nil {
			return fmt.Errorf("jobs: %w", err)
		}
		if cur != id {
			return ErrTenantMismatch
		}
		return nil
	case globalScoped:
		return nil
	default:
		return ErrNoScope
	}
}

type workerMiddleware struct{ river.MiddlewareDefaults }

// Work restores the tenant from the job's encoded args. A job whose
// store_id is present but malformed or zero is cancelled: retrying cannot
// fix it, and running it without a tenant would be wrong.
func (*workerMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	ctx, err := RestoreTenant(ctx, job.EncodedArgs)
	if err != nil {
		return river.JobCancel(fmt.Errorf("%w (kind %s, id %d)", err, job.Kind, job.ID))
	}
	return doInner(ctx)
}

// RestoreTenant returns ctx with the store from encoded job args, or ctx
// unchanged when the args carry no store_id (a global job). Any tenant
// already in ctx is cleared first, so a global job never inherits one.
func RestoreTenant(ctx context.Context, encodedArgs []byte) (context.Context, error) {
	var probe struct {
		StoreID *json.RawMessage `json:"store_id"`
	}
	if err := json.Unmarshal(encodedArgs, &probe); err != nil {
		return ctx, fmt.Errorf("jobs: decode args: %w", err)
	}
	if probe.StoreID == nil {
		return tenant.WithStore(ctx, tenant.StoreID{}), nil
	}
	var id tenant.StoreID
	if err := json.Unmarshal(*probe.StoreID, &id); err != nil {
		return ctx, fmt.Errorf("jobs: store_id: %w", err)
	}
	if id.IsZero() {
		return ctx, fmt.Errorf("jobs: store_id: %w", tenant.ErrNoTenant)
	}
	return tenant.WithStore(ctx, id), nil
}

// Inserter enqueues jobs inside the caller's transaction (ADR-0003).
// *river.Client[pgx.Tx] implements it.
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}
