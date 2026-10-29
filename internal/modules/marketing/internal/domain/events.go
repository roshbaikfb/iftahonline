package domain

import "github.com/roshbaikfb/iftahonline/internal/platform/event"

// EventPixelsChanged is the type of PixelsChanged.
const EventPixelsChanged = "marketing.pixels.changed"

// PixelsChanged is published when a store saves or removes a pixel, or
// changes its consent mode: what its pages load changed (the
// storefront's caches and the edge follow).
type PixelsChanged struct {
	event.Meta
	// Provider is the pixel that changed; empty for the consent mode.
	Provider Provider `json:"provider,omitempty"`
}
