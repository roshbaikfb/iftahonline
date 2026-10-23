package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// EventPaymentStatusChanged is the type of PaymentStatusChanged.
const EventPaymentStatusChanged = "payments.payment.status_changed"

// PaymentStatusChanged is published when a payment's status changes.
type PaymentStatusChanged struct {
	event.Meta
	PaymentID ulid.ID `json:"payment_id"`
	OrderID   ulid.ID `json:"order_id"`
	Status    Status  `json:"status"`
}
