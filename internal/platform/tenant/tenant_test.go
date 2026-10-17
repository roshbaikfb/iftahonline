package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestFromContext(t *testing.T) {
	t.Parallel()
	id := tenant.StoreID{ID: ulid.NewGenerator().New()}
	tests := []struct {
		name    string
		ctx     context.Context
		want    tenant.StoreID
		wantErr error
	}{
		{"set", tenant.WithStore(t.Context(), id), id, nil},
		{"missing", t.Context(), tenant.StoreID{}, tenant.ErrNoTenant},
		{"zero", tenant.WithStore(t.Context(), tenant.StoreID{}), tenant.StoreID{}, tenant.ErrNoTenant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tenant.FromContext(tt.ctx)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("got %v, %v; want %v, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParseStoreID(t *testing.T) {
	t.Parallel()
	id, err := tenant.ParseStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil || id.String() != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("got %v, %v", id, err)
	}
	if _, err := tenant.ParseStoreID("bad"); !errors.Is(err, ulid.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
