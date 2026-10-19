package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Recorder writes the audit log. It is separate from Service so other
// modules can be given one before the identity service exists (tenancy
// is built first because identity's sign-up needs it).
type Recorder struct{ ids *ulid.Generator }

// NewRecorder returns a recorder.
func NewRecorder(ids *ulid.Generator) *Recorder { return &Recorder{ids: ids} }

var _ audit.Recorder = (*Recorder)(nil)

// RecordTx implements audit.Recorder: the store is the tenant in ctx, the
// actor the principal in ctx (none for system actions), the network the
// caller's IP prefix.
func (s *Recorder) RecordTx(ctx context.Context, tx pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	var meta []byte
	if len(e.Metadata) > 0 {
		var err error
		if meta, err = json.Marshal(e.Metadata); err != nil {
			return fmt.Errorf("identity: audit metadata: %w", err)
		}
	}
	var actor *ulid.ID
	if p, ok := authz.PrincipalFrom(ctx); ok {
		actor = &p.UserID
	}
	return store.InsertAudit(ctx, tx, store.AuditEntry{
		ID: s.ids.New(), ActorUserID: actor, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID,
		IPPrefix: clientinfo.IPPrefix(metaFrom(ctx).IP), Metadata: meta,
	})
}

// AuditPage is one page of the log, newest first.
type AuditPage struct {
	Items      []store.AuditEntry
	NextCursor string // empty on the last page
}

// Audit page sizes.
const (
	DefaultAuditLimit = 50
	MaxAuditLimit     = 200
)

var errBadCursor = errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})

// ListAudit returns a page of the current store's log. The caller has
// already authorized audit.read.
func (s *Service) ListAudit(ctx context.Context, cursor string, limit int) (AuditPage, error) {
	if limit <= 0 {
		limit = DefaultAuditLimit
	}
	if limit > MaxAuditLimit {
		return AuditPage{}, errs.Validation(errs.FieldError{Field: "limit", Code: "too_large", Message: fmt.Sprintf("limit must be at most %d", MaxAuditLimit)})
	}
	var beforeAt *time.Time
	var beforeID *ulid.ID
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return AuditPage{}, errBadCursor
		}
		beforeAt, beforeID = &at, &id
	}
	var items []store.AuditEntry
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		items, err = store.ListAudit(ctx, tx, beforeAt, beforeID, limit+1)
		return err
	})
	if err != nil {
		return AuditPage{}, err
	}
	page := AuditPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
	}
	return page, nil
}

func encodeCursor(at time.Time, id ulid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + ":" + id.String()))
}

func decodeCursor(c string) (time.Time, ulid.ID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, ulid.ID{}, err
	}
	us, ids, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, ulid.ID{}, fmt.Errorf("identity: cursor")
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return time.Time{}, ulid.ID{}, err
	}
	id, err := ulid.Parse(ids)
	if err != nil {
		return time.Time{}, ulid.ID{}, err
	}
	return time.UnixMicro(n).UTC(), id, nil
}

// AuditPartitionsArgs keeps audit_log partitions ahead of time (daily).
type AuditPartitionsArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (AuditPartitionsArgs) Kind() string { return "identity.audit.partitions" }

// AuditPartitionsMonthsAhead is how far ahead partitions exist.
const AuditPartitionsMonthsAhead = 3

// AuditPartitionsWorker runs AuditPartitionsArgs.
type AuditPartitionsWorker struct {
	river.WorkerDefaults[AuditPartitionsArgs]
	DB store.Tx
}

// Work implements river.Worker.
func (w *AuditPartitionsWorker) Work(ctx context.Context, _ *river.Job[AuditPartitionsArgs]) error {
	_, err := store.EnsureAuditPartitions(ctx, w.DB, AuditPartitionsMonthsAhead)
	return err
}

// AuditPartitionsPeriodic runs the job daily and once at worker start.
func AuditPartitionsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return AuditPartitionsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
