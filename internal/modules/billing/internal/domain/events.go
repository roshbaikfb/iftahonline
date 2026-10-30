package domain

import "github.com/roshbaikfb/iftahonline/internal/platform/event"

// EventSubscriptionChanged is published in the transaction that changes
// a store's subscription: what its plan grants may have changed, so the
// grants cached by entitlements and the storefront's pages follow
// (ADR-0050). It names no plan.
const EventSubscriptionChanged = "billing.subscription.changed"

// SubscriptionChanged is EventSubscriptionChanged's payload.
type SubscriptionChanged struct {
	event.Meta
	Status Status `json:"status"`
}
