package domain_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
)

func TestOrderPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		order string
		st    domain.ShipmentStatus
		want  string
	}{
		{"ready_to_ship", domain.ShipmentInTransit, "shipped"},
		{"ready_to_ship", domain.ShipmentDelivered, "shipped delivered"},
		{"ready_to_ship", domain.ShipmentReturned, "shipped failed_delivery returned"},
		{"shipped", domain.ShipmentOutForDelivery, ""},
		{"shipped", domain.ShipmentFailedAttempt, "failed_delivery"},
		{"failed_delivery", domain.ShipmentOutForDelivery, "shipped"},
		{"failed_delivery", domain.ShipmentReturned, "returned"},
		{"delivered", domain.ShipmentReturned, "returned"},
		{"delivered", domain.ShipmentDelivered, ""},
		{"cancelled", domain.ShipmentDelivered, ""},
		{"ready_to_ship", domain.ShipmentCreated, ""},
		{"ready_to_ship", domain.ShipmentCancelled, ""},
	}
	for _, tt := range tests {
		if got := strings.Join(domain.OrderPath(tt.order, tt.st), " "); got != tt.want {
			t.Errorf("OrderPath(%s, %s) = %q, want %q", tt.order, tt.st, got, tt.want)
		}
	}
	if !domain.ShipmentDelivered.Final() || domain.ShipmentInTransit.Final() || domain.ShipmentFailed.Live() || !domain.ShipmentRequested.Live() {
		t.Fatal("status predicates")
	}
}

func TestShipmentStatuses(t *testing.T) {
	t.Parallel()
	for st, want := range map[domain.ShipmentStatus][3]bool{ // final, live, tracked
		domain.ShipmentRequested: {false, true, false}, domain.ShipmentCreated: {false, true, true},
		domain.ShipmentInTransit: {false, true, true}, domain.ShipmentDelivered: {true, true, true},
		domain.ShipmentReturned: {true, true, true}, domain.ShipmentCancelled: {true, false, true},
		domain.ShipmentFailed: {true, false, false},
	} {
		if got := [3]bool{st.Final(), st.Live(), st.Tracked()}; got != want {
			t.Errorf("%s: final/live/tracked = %v, want %v", st, got, want)
		}
	}
}

func TestAPIKeyAndSecret(t *testing.T) {
	t.Parallel()
	if k, err := domain.CleanAPIKey("  key-123\n"); err != nil || k != "key-123" {
		t.Fatalf("clean: %q %v", k, err)
	}
	for _, bad := range []string{"", " ", "a b", strings.Repeat("k", domain.MaxAPIKeyLen+1), "k\x00"} {
		if _, err := domain.CleanAPIKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	h := domain.SecretHash("s3cret")
	if !domain.SecretMatches("s3cret", h) || domain.SecretMatches("s3cre", h) || domain.SecretMatches("", domain.SecretHash("")) {
		t.Fatal("secret matching")
	}
}
