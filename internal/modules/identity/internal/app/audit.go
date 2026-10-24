package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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
func (s *Service) ListAudit(ctx context.Context, after string, limit int) (AuditPage, error) {
	if limit <= 0 {
		limit = DefaultAuditLimit
	}
	if limit > MaxAuditLimit {
		return AuditPage{}, errs.Validation(errs.FieldError{Field: "limit", Code: "too_large", Message: fmt.Sprintf("limit must be at most %d", MaxAuditLimit)})
	}
	var beforeAt *time.Time
	var beforeID *ulid.ID
	if after != "" {
		at, id, err := cursor.Decode(after)
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
		page.NextCursor = cursor.Encode(last.OccurredAt, last.ID)
	}
	return page, nil
}
