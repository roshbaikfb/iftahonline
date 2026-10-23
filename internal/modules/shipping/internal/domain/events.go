package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Event types.
const (
	EventRatesChanged          = "shipping.rates.changed"
	EventShipmentStatusChanged = "shipping.shipment.status_changed"
)

// RatesChanged is published when the store's settings or a zone is
// saved or deleted: every cached quote of the store is stale.
type RatesChanged struct {
	event.Meta
}

// ShipmentStatusChanged is published when a carrier moves a shipment.
type ShipmentStatusChanged struct {
	event.Meta
	ShipmentID ulid.ID        `json:"shipment_id"`
	OrderID    ulid.ID        `json:"order_id"`
	Status     ShipmentStatus `json:"status"`
}
