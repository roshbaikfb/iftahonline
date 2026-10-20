package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Event types the storefront publishes (ADR-0015, ADR-0016). Caches of
// rendered pages subscribe to them.
const (
	EventThemeChanged = "storefront.theme.changed"
	EventPageChanged  = "storefront.page.changed"
)

// ThemeChanged is published when the store's theme is saved: every page
// looks different.
type ThemeChanged struct {
	event.Meta
}

// PageChanged is published when a page is saved or deleted.
// PreviousSlug is set when a landing page moved, so the old URL can be
// purged too.
type PageChanged struct {
	event.Meta
	PageID       ulid.ID  `json:"page_id"`
	Kind         PageKind `json:"kind"`
	Slug         string   `json:"slug"`
	PreviousSlug string   `json:"previous_slug,omitempty"`
	Deleted      bool     `json:"deleted,omitempty"`
}
