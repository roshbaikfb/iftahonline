package domain

import "github.com/roshbaikfb/iftahonline/internal/platform/event"

// Event types.
const (
	EventRatesChanged = "shipping.rates.changed"
)

// RatesChanged is published when the store's settings or a zone is
// saved or deleted: every cached quote of the store is stale.
type RatesChanged struct {
	event.Meta
}
