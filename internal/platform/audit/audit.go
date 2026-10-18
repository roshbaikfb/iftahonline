// Package audit defines how modules record store actions in the
// append-only audit log. The identity module owns the table and implements
// Recorder; other modules receive a Recorder in their constructor, so they
// can audit inside their own transactions without importing identity
// (which would create an import cycle: identity's sign-up calls tenancy).
package audit

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// Entry is one audited action. The store comes from tenant.FromContext and
// the actor from authz.PrincipalFrom; neither is taken from the entry.
//
// Metadata must not carry secrets or personal data (tokens, phone numbers,
// addresses): the log is kept for years and shown to store staff.
type Entry struct {
	Action     string // "<resource>.<verb>", e.g. "domain.added"
	TargetType string // e.g. "domain"
	TargetID   string // public ID of the target
	Metadata   map[string]any
}

// Recorder writes entries in the caller's tenant transaction, so an action
// and its audit record commit or roll back together.
type Recorder interface {
	RecordTx(ctx context.Context, tx pgx.Tx, e Entry) error
}

var actionRE = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)

// Validate checks the action naming convention.
func (e Entry) Validate() error {
	if !actionRE.MatchString(e.Action) {
		return fmt.Errorf("audit: invalid action %q", e.Action)
	}
	return nil
}

// Nop discards entries. For tests and roles that never audit.
type Nop struct{}

// RecordTx implements Recorder.
func (Nop) RecordTx(context.Context, pgx.Tx, Entry) error { return nil }
