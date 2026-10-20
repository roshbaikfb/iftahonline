// Package event defines the base shape of domain events, the second way
// modules talk to each other (CLAUDE.md rule 2). Events are delivered
// through River jobs inserted in the same transaction as the state change
// (ADR-0003); this package only defines the envelope.
package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrInvalidType means an event type is not "<module>.<entity>.<verb>".
var ErrInvalidType = errors.New("event: invalid type")

// Event is implemented by every domain event. Embed Meta to satisfy it.
type Event interface {
	EventMeta() Meta
}

// Meta is the envelope every event carries.
type Meta struct {
	ID         ulid.ID        `json:"id"`
	Type       string         `json:"type"`    // "<module>.<entity>.<verb>", e.g. "catalog.product.created"
	Version    int            `json:"version"` // schema version of the payload, starting at 1
	StoreID    tenant.StoreID `json:"store_id"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// EventMeta implements Event.
func (m Meta) EventMeta() Meta { return m }

// NewMeta builds an envelope with a fresh ID. typ must be three lower-case
// dot-separated segments of [a-z0-9_]; version must be >= 1.
func NewMeta(gen *ulid.Generator, now time.Time, store tenant.StoreID, typ string, version int) (Meta, error) {
	if err := ValidateType(typ); err != nil {
		return Meta{}, err
	}
	if version < 1 {
		return Meta{}, fmt.Errorf("event: version must be >= 1, got %d", version)
	}
	if store.IsZero() {
		return Meta{}, tenant.ErrNoTenant
	}
	return Meta{ID: gen.New(), Type: typ, Version: version, StoreID: store, OccurredAt: now.UTC()}, nil
}

// MetaFor builds an envelope for the store in ctx, stamped now.
func MetaFor(ctx context.Context, gen *ulid.Generator, typ string, version int) (Meta, error) {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return Meta{}, fmt.Errorf("event: %s: %w", typ, err)
	}
	return NewMeta(gen, time.Now(), store, typ, version)
}

// ValidateType checks the "<module>.<entity>.<verb>" naming convention.
func ValidateType(typ string) error {
	parts := strings.Split(typ, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: %q", ErrInvalidType, typ)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("%w: %q", ErrInvalidType, typ)
		}
		for _, c := range p {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
				return fmt.Errorf("%w: %q", ErrInvalidType, typ)
			}
		}
	}
	return nil
}
