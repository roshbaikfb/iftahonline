package webhooks_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestURL(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	store, conn := tenant.StoreID{ID: ids.New()}, ids.New()
	want := "https://app.example.com/v1/webhooks/bosta/" + store.String() + "/" + conn.String()
	for _, base := range []string{"https://app.example.com", "https://app.example.com/"} {
		if got := webhooks.URL(base, "bosta", store, conn); got != want {
			t.Errorf("URL(%q) = %q, want %q", base, got, want)
		}
	}
}
