package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// EventPaymentStatusChanged is the type of PaymentStatusChanged.
const EventPaymentStatusChanged = "payments.payment.status_changed"

// EventConnectionChanged is the type of ConnectionChanged.
const EventConnectionChanged = "payments.connection.changed"

// ConnectionChanged is published when a store connects or disconnects a
// gateway: whether it takes cards changed (the storefront's caches
// follow).
type ConnectionChanged struct {
	event.Meta
	ConnectionID ulid.ID `json:"connection_id"`
	Gateway      string  `json:"gateway"`
	Status       string  `json:"status"`
}

// PaymentStatusChanged is published when a payment's status changes.
type PaymentStatusChanged struct {
	event.Meta
	PaymentID ulid.ID `json:"payment_id"`
	OrderID   ulid.ID `json:"order_id"`
	Status    Status  `json:"status"`
}

// EventSettingsChanged is the type of SettingsChanged.
const EventSettingsChanged = "payments.settings.changed"

// SettingsChanged is published when the store changes which method its
// checkout starts on (the storefront's caches follow).
type SettingsChanged struct {
	event.Meta
	DefaultMethod string `json:"default_method"`
}
