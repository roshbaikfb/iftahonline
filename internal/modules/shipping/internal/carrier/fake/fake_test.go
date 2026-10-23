package fake_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/carriertest"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func harness(c *fake.Carrier) carriertest.Harness {
	return carriertest.Harness{
		Carrier: c, Creds: carrier.Credentials{APIKey: "merchant-key"}, BadCreds: carrier.Credentials{APIKey: fake.InvalidKey},
		Advance: func(t *testing.T, tracking string, to carrier.Status) {
			t.Helper()
			if err := c.Advance(tracking, to); err != nil {
				t.Fatal(err)
			}
		},
		Webhook: func(t *testing.T, tracking string) []byte {
			t.Helper()
			b, err := c.Webhook(tracking)
			if err != nil {
				t.Fatal(err)
			}
			return b
		},
		Settlements: true,
	}
}

func TestContract(t *testing.T) {
	t.Parallel()
	carriertest.Run(t, harness(fake.New()))
}

// TestAccountsAreSeparate: one merchant's key never sees another's
// shipments, and settlements are per account and per period.
func TestAccountsAreSeparate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := fake.New()
	a, b := carrier.Credentials{APIKey: "a"}, carrier.Credentials{APIKey: "b"}
	req := carriertest.Request(t)
	s, err := c.CreateShipment(ctx, a, req)
	if err != nil {
		t.Fatal(err)
	}
	if other, otherErr := c.CreateShipment(ctx, b, req); otherErr != nil || other.TrackingNumber == s.TrackingNumber {
		t.Fatalf("one reference in two accounts must be two shipments: %+v %v", other, otherErr)
	}
	if _, err = c.Track(ctx, b, s.TrackingNumber); !errors.Is(err, carrier.ErrNotFound) {
		t.Fatalf("b tracks a's shipment: %v", err)
	}
	amount, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if err = c.Settle(s.TrackingNumber, carrier.Settlement{Amount: amount, SettledAt: day, Reference: "payout-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.CODSettlements(ctx, a, day.Add(-time.Hour), day.Add(time.Hour))
	if err != nil || len(got) != 1 || got[0].TrackingNumber != s.TrackingNumber || got[0].Amount.Amount() != 52000 {
		t.Fatalf("a's settlements: %+v %v", got, err)
	}
	if got, err = c.CODSettlements(ctx, a, day.Add(time.Hour), day.Add(2*time.Hour)); err != nil || len(got) != 0 {
		t.Fatalf("outside the period: %+v %v", got, err)
	}
	if got, err = c.CODSettlements(ctx, b, day.Add(-time.Hour), day.Add(time.Hour)); err != nil || len(got) != 0 {
		t.Fatalf("b sees a's settlements: %+v %v", got, err)
	}
	if r, ok := c.Request(s.TrackingNumber); !ok || r.Reference != req.Reference || c.Calls("create") != 2 {
		t.Fatalf("request %+v %v, creates %d", r, ok, c.Calls("create"))
	}
	if err = c.Advance("nope", carrier.StatusDelivered); !errors.Is(err, carrier.ErrNotFound) {
		t.Fatal(err)
	}
}

// TestCredentialsNeverPrint: a key formatted or logged by mistake shows
// as redacted.
func TestCredentialsNeverPrint(t *testing.T) {
	t.Parallel()
	c := carrier.Credentials{APIKey: "very-secret"}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v %+v %#v %s", c, c, c, c), c.LogValue().String()} {
		if strings.Contains(s, "very-secret") {
			t.Fatalf("leaked: %s", s)
		}
	}
}
