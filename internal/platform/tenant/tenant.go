// Package tenant carries the current store through context.Context.
//
// The store ID used by every tenant-scoped query MUST come from
// FromContext (CLAUDE.md rule 5, ADR-0002). It is set once, by the HTTP
// tenant-resolution middleware or by a job from its arguments, and is
// never taken from request bodies or query strings by handlers.
package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNoTenant means the context carries no (or a zero) store ID.
var ErrNoTenant = errors.New("tenant: no store in context")

// StoreID identifies a store (tenant). It embeds ulid.ID, so it has
// String, JSON/text and database encodings.
type StoreID struct{ ulid.ID }

// ParseStoreID parses a public store ID.
func ParseStoreID(s string) (StoreID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return StoreID{}, fmt.Errorf("tenant: store id: %w", err)
	}
	return StoreID{id}, nil
}

type ctxKey struct{}

// WithStore returns a copy of ctx that carries id.
func WithStore(ctx context.Context, id StoreID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the current store. It fails with ErrNoTenant rather
// than returning a zero ID that could match nothing or, worse, everything.
func FromContext(ctx context.Context) (StoreID, error) {
	id, ok := ctx.Value(ctxKey{}).(StoreID)
	if !ok || id.IsZero() {
		return StoreID{}, ErrNoTenant
	}
	return id, nil
}
