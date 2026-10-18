package audit_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"store.created", "domain.primary_set"} {
		if err := (audit.Entry{Action: ok}).Validate(); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "created", "Store.Created", "a.b.c", "store-created", "store.created "} {
		if err := (audit.Entry{Action: bad}).Validate(); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if err := (audit.Nop{}).RecordTx(t.Context(), nil, audit.Entry{}); err != nil {
		t.Fatal(err)
	}
}
