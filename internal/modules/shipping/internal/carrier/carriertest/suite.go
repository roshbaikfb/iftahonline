// Package carriertest is the contract every carrier adapter passes
// (ADR-0026). The same suite runs against the fake carrier, against
// Bosta's in-process stand-in in every build, and against Bosta's
// sandbox when its key is set. Cases that need to move a parcel along
// (which only a courier can do on a sandbox) run when the harness can.
package carriertest

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Harness is one adapter under test.
type Harness struct {
	Carrier  carrier.Carrier
	Creds    carrier.Credentials
	BadCreds carrier.Credentials
	// Advance moves a shipment at the carrier; nil when the harness
	// cannot (a sandbox), which skips the cases that need it.
	Advance func(t *testing.T, tracking string, to carrier.Status)
	// Webhook is the body the carrier sends for the shipment's last
	// change; nil when the harness cannot produce one.
	Webhook func(t *testing.T, tracking string) []byte
	// Settlements: the carrier has a settlements API.
	Settlements bool
}

// Request is a valid shipment request with a fresh reference.
func Request(t *testing.T) carrier.ShipmentRequest {
	t.Helper()
	cod, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return carrier.ShipmentRequest{
		Reference: ulid.NewGenerator().New().String(),
		Receiver:  carrier.Receiver{Name: "منى أحمد علي", Phone: "+201012345678"},
		Address:   carrier.Address{Governorate: "EG-C", City: "مدينة نصر", Line: "12 شارع عباس العقاد، الدور الثالث"},
		COD:       cod, Items: 2, Description: "عطر العود الملكي × 2", Notes: "اتصل قبل الوصول",
	}
}

// Run runs the contract.
func Run(t *testing.T, h Harness) {
	ctx := t.Context()
	c := h.Carrier

	t.Run("verify", func(t *testing.T) {
		if err := c.Verify(ctx, h.Creds); err != nil {
			t.Fatalf("valid credentials: %v", err)
		}
		if err := c.Verify(ctx, h.BadCreds); !errors.Is(err, carrier.ErrAuth) {
			t.Fatalf("bad credentials: %v, want ErrAuth", err)
		}
	})

	t.Run("create is idempotent by reference", func(t *testing.T) {
		req := Request(t)
		first, err := c.CreateShipment(ctx, h.Creds, req)
		if err != nil || first.TrackingNumber == "" {
			t.Fatalf("create: %+v %v", first, err)
		}
		again, err := c.CreateShipment(ctx, h.Creds, req)
		if err != nil || again.TrackingNumber != first.TrackingNumber {
			t.Fatalf("second create with one reference: %+v %v, want %s", again, err, first.TrackingNumber)
		}
		other := Request(t)
		if o, otherErr := c.CreateShipment(ctx, h.Creds, other); otherErr != nil || o.TrackingNumber == first.TrackingNumber {
			t.Fatalf("another reference: %+v %v", o, otherErr)
		}
		tr, err := c.Track(ctx, h.Creds, first.TrackingNumber)
		if err != nil || tr.Status != carrier.StatusCreated {
			t.Fatalf("track a new shipment: %+v %v", tr, err)
		}
		pdf, err := c.Label(ctx, h.Creds, first.TrackingNumber)
		if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
			t.Fatalf("label: %d bytes %v", len(pdf), err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		bad := Request(t)
		bad.Receiver.Phone = ""
		if _, err := c.CreateShipment(ctx, h.Creds, bad); !errors.Is(err, carrier.ErrRejected) {
			t.Errorf("no phone: %v, want ErrRejected", err)
		}
		if _, err := c.CreateShipment(ctx, h.BadCreds, Request(t)); !errors.Is(err, carrier.ErrAuth) {
			t.Errorf("bad credentials: %v, want ErrAuth", err)
		}
		if _, err := c.Track(ctx, h.Creds, "0000000000"); !errors.Is(err, carrier.ErrNotFound) {
			t.Errorf("unknown tracking number: %v, want ErrNotFound", err)
		}
	})

	t.Run("cancel before pickup", func(t *testing.T) {
		s, err := c.CreateShipment(ctx, h.Creds, Request(t))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 { // cancelling twice succeeds
			if err = c.Cancel(ctx, h.Creds, s.TrackingNumber); err != nil {
				t.Fatalf("cancel: %v", err)
			}
		}
		if tr, trErr := c.Track(ctx, h.Creds, s.TrackingNumber); trErr != nil || tr.Status != carrier.StatusCancelled {
			t.Fatalf("after cancel: %+v %v", tr, trErr)
		}
	})

	t.Run("settlements", func(t *testing.T) {
		_, err := c.CODSettlements(ctx, h.Creds, time.Now().Add(-7*24*time.Hour), time.Now())
		if h.Settlements && err != nil {
			t.Fatalf("settlements: %v", err)
		}
		if !h.Settlements && !errors.Is(err, carrier.ErrUnsupported) {
			t.Fatalf("settlements: %v, want ErrUnsupported", err)
		}
	})

	t.Run("webhooks parse", func(t *testing.T) {
		if _, err := c.ParseWebhook([]byte(`not json`)); !errors.Is(err, carrier.ErrBadWebhook) {
			t.Fatalf("garbage: %v, want ErrBadWebhook", err)
		}
		if _, err := c.ParseWebhook([]byte(`{}`)); !errors.Is(err, carrier.ErrBadWebhook) {
			t.Fatalf("empty object: %v, want ErrBadWebhook", err)
		}
	})

	if h.Advance == nil {
		t.Log("the harness cannot move parcels: delivery cases skipped")
		return
	}
	t.Run("the parcel moves", func(t *testing.T) {
		s, err := c.CreateShipment(ctx, h.Creds, Request(t))
		if err != nil {
			t.Fatal(err)
		}
		var lastEvent string
		for _, to := range []carrier.Status{carrier.StatusInTransit, carrier.StatusOutForDelivery, carrier.StatusDelivered} {
			h.Advance(t, s.TrackingNumber, to)
			tr, trErr := c.Track(ctx, h.Creds, s.TrackingNumber)
			if trErr != nil || tr.Status != to {
				t.Fatalf("track after %s: %+v %v", to, tr, trErr)
			}
			if to == carrier.StatusInTransit {
				if cancelErr := c.Cancel(ctx, h.Creds, s.TrackingNumber); !errors.Is(cancelErr, carrier.ErrNotCancellable) {
					t.Fatalf("cancel in transit: %v, want ErrNotCancellable", cancelErr)
				}
			}
			if h.Webhook == nil {
				continue
			}
			body := h.Webhook(t, s.TrackingNumber)
			ev, parseErr := c.ParseWebhook(body)
			if parseErr != nil || ev.TrackingNumber != s.TrackingNumber || ev.EventID == "" || ev.EventID == lastEvent {
				t.Fatalf("webhook for %s: %+v %v (previous event %q)", to, ev, parseErr, lastEvent)
			}
			if again, againErr := c.ParseWebhook(body); againErr != nil || again.EventID != ev.EventID {
				t.Fatalf("a retried webhook changed its event id: %+v %v", again, againErr)
			}
			lastEvent = ev.EventID
		}
	})
}
