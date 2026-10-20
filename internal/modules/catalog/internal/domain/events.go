package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Event types the catalog publishes (ADR-0015). Payloads carry IDs, not
// state: a subscriber reads current state through the catalog's api.go
// when it needs it, so repeated or reordered deliveries are harmless.
const (
	EventProductChanged  = "catalog.product.changed"
	EventProductDeleted  = "catalog.product.deleted"
	EventCategoryChanged = "catalog.category.changed"
)

// Why a product changed.
const (
	ReasonCreated = "created"
	ReasonUpdated = "updated" // fields, variants, prices or categories
	ReasonImages  = "images"
	ReasonStock   = "stock" // on-hand set by the merchant, or sold out / back in stock
)

// Why a category changed.
const (
	CategoryCreated = "created"
	CategoryUpdated = "updated"
	CategoryDeleted = "deleted"
)

// ProductChanged is published when anything a shopper can see on a live
// product may have changed.
type ProductChanged struct {
	event.Meta
	ProductID ulid.ID `json:"product_id"`
	Reason    string  `json:"reason"`
}

// ProductDeleted is published when a product is deleted.
type ProductDeleted struct {
	event.Meta
	ProductID ulid.ID `json:"product_id"`
}

// CategoryChanged is published when a category is created, changed or
// deleted.
type CategoryChanged struct {
	event.Meta
	CategoryID ulid.ID `json:"category_id"`
	Reason     string  `json:"reason"`
}
