// Package carrier is the contract every courier adapter meets (ADR-0026):
// create a shipment, fetch its label, track it, cancel it, list cash-on-
// delivery settlements, and read the carrier's webhook deliveries. The
// fake adapter (carrier/fake) and Bosta (carrier/bosta) pass one shared
// contract suite (carrier/carriertest).
//
// Adapters are stateless: the merchant's credentials come with each call,
// opened from the store's connection by the caller.
package carrier

import (
	"context"
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Errors. Match with errors.Is.
var (
	// ErrAuth: the carrier refused the credentials. Retrying cannot help.
	ErrAuth = errors.New("carrier: credentials refused")
	// ErrRejected: the carrier refused the request itself (an address it
	// does not serve, a bad phone). Retrying cannot help.
	ErrRejected = errors.New("carrier: request rejected")
	// ErrNotFound: the carrier knows no such shipment.
	ErrNotFound = errors.New("carrier: shipment not found")
	// ErrUnsupported: this carrier does not offer the operation through
	// its API (Bosta has no settlements API, for one).
	ErrUnsupported = errors.New("carrier: not supported by this carrier")
	// ErrNotCancellable: the shipment is too far along to cancel.
	ErrNotCancellable = errors.New("carrier: shipment can no longer be cancelled")
	// ErrBadWebhook: a webhook body that does not parse as the carrier's.
	ErrBadWebhook = errors.New("carrier: unreadable webhook")
)

// Status is where a shipment is, in our words. Adapters map the
// carrier's own states onto it.
type Status string

// Statuses.
const (
	StatusCreated        Status = "created"          // known to the carrier, not yet picked up
	StatusInTransit      Status = "in_transit"       // picked up, on its way
	StatusOutForDelivery Status = "out_for_delivery" // with the courier today
	StatusDelivered      Status = "delivered"        // handed over (and cash collected)
	StatusFailedAttempt  Status = "failed_attempt"   // a delivery attempt failed; may be retried
	StatusReturned       Status = "returned"         // back with the merchant
	StatusCancelled      Status = "cancelled"        // cancelled before pickup
	StatusUnknown        Status = "unknown"          // a state the adapter does not know
)

// Final reports whether no further change is expected.
func (s Status) Final() bool {
	return s == StatusDelivered || s == StatusReturned || s == StatusCancelled
}

// Credentials are the merchant's account at the carrier.
type Credentials struct {
	APIKey string `json:"api_key"`
}

// ShipmentRequest asks the carrier to deliver one order.
type ShipmentRequest struct {
	// Reference is our shipment ID: the carrier keeps it, and creating
	// twice with one Reference makes one shipment (Create is retried).
	Reference string
	Receiver  Receiver
	Address   Address
	// COD is what the courier collects; zero for a prepaid order.
	COD         money.Money
	Items       int
	Description string // what is inside, for the courier
	Notes       string // the shopper's notes
}

// Receiver is who receives the parcel.
type Receiver struct {
	Name  string
	Phone string // E.164
}

// Address is where the parcel goes.
type Address struct {
	Governorate string // ISO 3166-2, e.g. EG-C
	City        string // as the shopper typed it
	Line        string // street, building, floor, landmark
}

// Shipment is a shipment as the carrier created it.
type Shipment struct {
	TrackingNumber string
	ProviderID     string // the carrier's own ID, when it differs
}

// Tracking is a shipment's state at the carrier.
type Tracking struct {
	Status   Status
	RawState string    // the carrier's own state, for support
	At       time.Time // when the carrier last changed it (zero if unknown)
}

// Settlement is cash the carrier paid out for one shipment.
type Settlement struct {
	TrackingNumber string
	Amount         money.Money
	SettledAt      time.Time
	Reference      string // the payout's reference, e.g. a transfer ID
}

// WebhookEvent is what a webhook says. It is a hint: the caller tracks
// the shipment before changing anything (ADR-0024).
type WebhookEvent struct {
	// EventID is stable across the carrier's retries of one delivery and
	// different for each real change: the once-only key.
	EventID        string
	TrackingNumber string
}

// Carrier is one courier's API.
type Carrier interface {
	// Name is the carrier's ID in connections and webhook URLs, e.g.
	// "bosta".
	Name() string
	// Verify checks creds with a cheap authenticated call.
	Verify(ctx context.Context, creds Credentials) error
	// CreateShipment creates a shipment; repeating it with the same
	// Reference returns the first shipment.
	CreateShipment(ctx context.Context, creds Credentials, req ShipmentRequest) (Shipment, error)
	// Label returns the shipment's label (airway bill) as a PDF.
	Label(ctx context.Context, creds Credentials, trackingNumber string) ([]byte, error)
	// Track returns the shipment's current state.
	Track(ctx context.Context, creds Credentials, trackingNumber string) (Tracking, error)
	// Cancel cancels a shipment that has not been picked up. Cancelling
	// a cancelled shipment succeeds.
	Cancel(ctx context.Context, creds Credentials, trackingNumber string) error
	// CODSettlements lists the cash paid out in [from, to); ErrUnsupported
	// when the carrier has no API for it.
	CODSettlements(ctx context.Context, creds Credentials, from, to time.Time) ([]Settlement, error)
	// ParseWebhook reads a webhook body (already verified by the caller);
	// ErrBadWebhook when it is not the carrier's.
	ParseWebhook(body []byte) (WebhookEvent, error)
}
