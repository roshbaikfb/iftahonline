package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Event types orders publish (ADR-0015). Payloads carry IDs and statuses,
// never the customer's contact details: subscribers read what they need
// through the orders api.go.
const (
	EventOrderPlaced         = "orders.order.placed"
	EventOrderStatusChanged  = "orders.order.status_changed"
	EventOrderPaymentChanged = "orders.order.payment_changed"
)

// OrderRef is what every orders event carries: which order. A
// subscriber that only needs to know which order changed decodes any of
// them into it.
type OrderRef struct {
	event.Meta
	OrderID ulid.ID `json:"order_id"`
}

// OrderPlaced is published when an order is placed.
type OrderPlaced struct {
	event.Meta
	OrderID ulid.ID `json:"order_id"`
	Source  Source  `json:"source"`
}

// OrderStatusChanged is published on every transition.
type OrderStatusChanged struct {
	event.Meta
	OrderID ulid.ID `json:"order_id"`
	From    Status  `json:"from"`
	To      Status  `json:"to"`
}

// OrderPaymentChanged is published when a card order's payment status
// changes, or a cash order's deposit status (Deposit, ADR-0067).
type OrderPaymentChanged struct {
	event.Meta
	OrderID ulid.ID       `json:"order_id"`
	From    PaymentStatus `json:"from"`
	To      PaymentStatus `json:"to"`
	Deposit bool          `json:"deposit,omitempty"`
}
