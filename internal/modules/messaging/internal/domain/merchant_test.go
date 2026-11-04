package domain_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
)

// TestMerchantTemplates: each merchant notice is the template submitted
// to Meta, with its number of values (docs/modules/messaging.md).
func TestMerchantTemplates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		k      domain.MerchantKind
		name   string
		params int
	}{
		{domain.MerchantPaymentFailed, "billing_payment_failed_v1", 4},
		{domain.MerchantSuspended, "billing_store_suspended_v1", 1},
		{domain.MerchantFree, "billing_store_free_v1", 2},
		{domain.MerchantPaid, "billing_payment_received_v1", 3},
	}
	for _, tt := range tests {
		if name, params, ok := domain.MerchantTemplate(tt.k); !ok || name != tt.name || params != tt.params {
			t.Errorf("%s: %q %d %v", tt.k, name, params, ok)
		}
	}
	if _, _, ok := domain.MerchantTemplate("billing_unknown"); ok {
		t.Error("an unknown kind has no template")
	}
}
