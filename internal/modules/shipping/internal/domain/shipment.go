package domain

import (
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ShipmentStatus is where a shipment is (ADR-0027).
type ShipmentStatus string

// Shipment statuses. Requested and failed are ours (asked, refused); the
// rest follow the carrier.
const (
	ShipmentRequested      ShipmentStatus = "requested"
	ShipmentCreated        ShipmentStatus = "created"
	ShipmentInTransit      ShipmentStatus = "in_transit"
	ShipmentOutForDelivery ShipmentStatus = "out_for_delivery"
	ShipmentDelivered      ShipmentStatus = "delivered"
	ShipmentFailedAttempt  ShipmentStatus = "failed_attempt"
	ShipmentReturned       ShipmentStatus = "returned"
	ShipmentCancelled      ShipmentStatus = "cancelled"
	ShipmentFailed         ShipmentStatus = "failed" // the carrier refused to create it
)

// Final reports whether the shipment will not change again.
func (s ShipmentStatus) Final() bool {
	return s == ShipmentDelivered || s == ShipmentReturned || s == ShipmentCancelled || s == ShipmentFailed
}

// Live reports whether the shipment holds its order (one live shipment
// per order).
func (s ShipmentStatus) Live() bool { return s != ShipmentCancelled && s != ShipmentFailed }

// Tracked reports whether the carrier knows the shipment.
func (s ShipmentStatus) Tracked() bool { return s != ShipmentRequested && s != ShipmentFailed }

// Event sources.
const (
	SourceAPI      = "api"      // what the carrier answered us
	SourceWebhook  = "webhook"  // a webhook, re-queried
	SourcePoll     = "poll"     // the scheduled poll
	SourceMerchant = "merchant" // a merchant's action
)

// Shipment is an order handed to a carrier.
type Shipment struct {
	ID                ulid.ID
	OrderID           ulid.ID
	ConnectionID      ulid.ID
	Carrier           string
	Status            ShipmentStatus
	TrackingNumber    string
	ProviderID        string
	COD               money.Money
	LabelKey          string // object key of the stored label; empty until fetched
	RawState          string // the carrier's last state, for support
	Failure           string // why the carrier refused it
	CancelRequestedAt *time.Time
	StatusAt          time.Time
	CreatedAt         time.Time
	Version           int
}

// Parcel is what a shopper may see of a shipment: who carries it, its
// tracking number and where it stands; never the address or the COD.
type Parcel struct {
	Carrier        string
	TrackingNumber string
	Status         ShipmentStatus
}

// ShipmentEvent is one entry of a shipment's timeline.
type ShipmentEvent struct {
	ID       ulid.ID
	At       time.Time
	Status   ShipmentStatus
	RawState string
	Source   string
	Note     string
}

// Order statuses, as the orders module names them (shipping cannot import
// it: orders prices deliveries through shipping).
const (
	OrderReadyToShip    = "ready_to_ship"
	OrderShipped        = "shipped"
	OrderDelivered      = "delivered"
	OrderFailedDelivery = "failed_delivery"
	OrderReturned       = "returned"
)

// OrderPath is the moves that bring an order in status order to where a
// shipment in status s says it is, in order. An order already there, or
// past it, needs none; an order the shipment does not explain (cancelled
// by the merchant, say) is left alone.
func OrderPath(order string, s ShipmentStatus) []string {
	var target string
	switch s {
	case ShipmentInTransit, ShipmentOutForDelivery:
		target = OrderShipped
	case ShipmentDelivered:
		target = OrderDelivered
	case ShipmentFailedAttempt:
		target = OrderFailedDelivery
	case ShipmentReturned:
		target = OrderReturned
	default:
		return nil
	}
	paths := map[string]map[string][]string{
		OrderReadyToShip: {
			OrderShipped: {OrderShipped}, OrderDelivered: {OrderShipped, OrderDelivered},
			OrderFailedDelivery: {OrderShipped, OrderFailedDelivery}, OrderReturned: {OrderShipped, OrderFailedDelivery, OrderReturned},
		},
		OrderShipped: {
			OrderDelivered: {OrderDelivered}, OrderFailedDelivery: {OrderFailedDelivery},
			OrderReturned: {OrderFailedDelivery, OrderReturned},
		},
		OrderFailedDelivery: {
			OrderShipped: {OrderShipped}, OrderDelivered: {OrderShipped, OrderDelivered}, OrderReturned: {OrderReturned},
		},
		OrderDelivered: {OrderReturned: {OrderReturned}},
	}
	return paths[order][target]
}

// ShipmentPage is one page of shipments, newest first.
type ShipmentPage struct {
	Items []Shipment
	Next  *Cursor
}

// Cursor is a keyset position in the shipments list.
type Cursor struct {
	At time.Time
	ID ulid.ID
}

// ShipmentQuery filters the list.
type ShipmentQuery struct {
	OrderID *ulid.ID
	Status  *ShipmentStatus
	After   *Cursor
	Limit   int
}
