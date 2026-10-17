package event_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

type productCreated struct {
	event.Meta
	ProductID ulid.ID `json:"product_id"`
}

func TestNewMeta(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	store := tenant.StoreID{ID: gen.New()}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("EET", 3*3600))
	tests := []struct {
		name    string
		store   tenant.StoreID
		typ     string
		version int
		wantErr error
	}{
		{"ok", store, "catalog.product.created", 1, nil},
		{"underscore digits", store, "order_v2.line_item.price_changed", 3, nil},
		{"two segments", store, "catalog.created", 1, event.ErrInvalidType},
		{"upper case", store, "Catalog.product.created", 1, event.ErrInvalidType},
		{"empty segment", store, "catalog..created", 1, event.ErrInvalidType},
		{"no store", tenant.StoreID{}, "catalog.product.created", 1, tenant.ErrNoTenant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := event.NewMeta(gen, now, tt.store, tt.typ, tt.version)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			var e event.Event = productCreated{Meta: m}
			got := e.EventMeta()
			if got.ID.IsZero() || got.Type != tt.typ || got.StoreID != store || got.OccurredAt.Location() != time.UTC {
				t.Fatalf("meta = %+v", got)
			}
		})
	}
	if _, err := event.NewMeta(gen, now, store, "a.b.c", 0); err == nil {
		t.Fatal("version 0 must fail")
	}
}
