package app_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
)

// TestParcel: what order tracking shows of an order's shipment: the
// newest one with a tracking number that is not cancelled or refused.
func TestParcel(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	order := f.orders.Add(t, "ready_to_ship")
	if _, ok, err := f.svc.Parcel(f.ctxA, order); err != nil || ok {
		t.Fatalf("no shipment yet: %v %v", ok, err)
	}
	sh := f.ship(t, order)
	p, ok, err := f.svc.Parcel(f.ctxA, order)
	if err != nil || !ok || p.Carrier != fake.Name || p.TrackingNumber != sh.TrackingNumber || p.Status != domain.ShipmentCreated {
		t.Fatalf("parcel %+v %v %v", p, ok, err)
	}
	if _, ok, err = f.svc.Parcel(f.ctxB, order); err != nil || ok {
		t.Fatalf("another store sees the parcel: %v %v", ok, err)
	}
	if _, err = f.svc.Cancel(f.ctxA, sh.ID); err != nil {
		t.Fatal(err)
	}
	if p, ok, err = f.svc.Parcel(f.ctxA, order); err != nil || (ok && p.Status == domain.ShipmentCancelled) {
		t.Fatalf("a cancelled shipment is not the parcel: %+v %v %v", p, ok, err)
	}
}
