package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// VerifyMaxAttempts with River's attempt^4 backoff spans about 68 hours.
const VerifyMaxAttempts = 16

// VerifyDomainArgs checks a pending custom domain's TXT record.
type VerifyDomainArgs struct {
	jobs.TenantArgs
	DomainID ulid.ID `json:"domain_id"`
}

// Kind implements river.JobArgs.
func (VerifyDomainArgs) Kind() string { return "tenancy.domain.verify" }

// InsertOpts implements river.JobArgsWithInsertOpts: one live check per
// domain, retried with backoff for about three days.
func (VerifyDomainArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: VerifyMaxAttempts,
		// Unique among live jobs only, so a later re-verification is allowed.
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled,
		}},
	}
}

// TXTResolver looks up TXT records. *net.Resolver implements it.
type TXTResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// VerifyWorker runs VerifyDomainArgs.
type VerifyWorker struct {
	river.WorkerDefaults[VerifyDomainArgs]
	DB            postgres.Beginner
	DNS           TXTResolver
	Svc           *Service
	LookupTimeout time.Duration // per DNS lookup
}

// Timeout implements river.Worker.
func (w *VerifyWorker) Timeout(*river.Job[VerifyDomainArgs]) time.Duration { return 30 * time.Second }

// errNotYet makes River retry with backoff.
var errNotYet = errors.New("tenancy: domain not verified yet")

// Work checks the record. Success verifies the domain and invalidates its
// cache entry; a miss is retried until the last attempt marks it failed.
// A domain that is no longer pending (deleted, verified) ends the job.
func (w *VerifyWorker) Work(ctx context.Context, job *river.Job[VerifyDomainArgs]) error {
	var d domain.Domain
	err := postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		var err error
		d, err = store.GetDomain(ctx, tx, job.Args.DomainID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Status != domain.DomainPending {
		return nil
	}

	name, _ := d.VerificationRecord()
	lctx, cancel := context.WithTimeout(ctx, w.LookupTimeout)
	values, lookupErr := w.DNS.LookupTXT(lctx, name)
	cancel()

	if lookupErr == nil && d.TXTProvesOwnership(values) {
		err = postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
			v, markErr := store.MarkVerified(ctx, tx, d.ID)
			switch {
			case errors.Is(markErr, store.ErrDomainTaken):
				// The unique violation aborted this transaction; record the
				// failure in a fresh one.
				return errTaken
			case errors.Is(markErr, domain.ErrNotFound):
				return nil
			case markErr != nil:
				return markErr
			}
			if auditErr := w.Svc.auditDomain(ctx, tx, "domain.verified", v); auditErr != nil {
				return auditErr
			}
			return w.Svc.invalidate(ctx, tx, v.Host)
		})
		if errors.Is(err, errTaken) {
			return w.markTaken(ctx, d.ID)
		}
		return err
	}

	reason := domain.ReasonTXTNotFound
	if dnsErr, ok := errors.AsType[*net.DNSError](lookupErr); lookupErr != nil && (!ok || !dnsErr.IsNotFound) {
		reason = domain.ReasonDNSError
	}
	final := job.Attempt >= job.MaxAttempts
	err = postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		var markErr error
		if final {
			_, markErr = store.MarkFailed(ctx, tx, d.ID, domain.ReasonTimedOut)
		} else {
			_, markErr = store.MarkCheckFailed(ctx, tx, d.ID, reason)
		}
		if errors.Is(markErr, domain.ErrNotFound) {
			return nil
		}
		return markErr
	})
	if err != nil || final {
		return err
	}
	return fmt.Errorf("%w: %s", errNotYet, reason)
}

var errTaken = errors.New("tenancy: domain taken")

// markTaken records that another store verified the host first.
func (w *VerifyWorker) markTaken(ctx context.Context, id ulid.ID) error {
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		_, err := store.MarkFailed(ctx, tx, id, domain.ReasonTaken)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	})
}

// InvalidateHostsArgs drops hosts from every cache layer.
type InvalidateHostsArgs struct {
	jobs.TenantArgs
	Hosts []string `json:"hosts"`
}

// Kind implements river.JobArgs.
func (InvalidateHostsArgs) Kind() string { return "tenancy.hosts.invalidate" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (InvalidateHostsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10}
}

// HostInvalidator deletes hosts from the shared cache and broadcasts the
// eviction to every instance's L1.
type HostInvalidator interface {
	Invalidate(ctx context.Context, hosts []string) error
}

// InvalidateWorker runs InvalidateHostsArgs.
type InvalidateWorker struct {
	river.WorkerDefaults[InvalidateHostsArgs]
	Cache HostInvalidator
}

// Timeout implements river.Worker.
func (*InvalidateWorker) Timeout(*river.Job[InvalidateHostsArgs]) time.Duration {
	return 10 * time.Second
}

// Work implements river.Worker.
func (w *InvalidateWorker) Work(ctx context.Context, job *river.Job[InvalidateHostsArgs]) error {
	return w.Cache.Invalidate(ctx, job.Args.Hosts)
}
