package domain

import (
	"time"
)

// Drafts and previews (ADR-0036).
const (
	// PreviewTTL is how long a preview link opens the shop with the
	// drafts.
	PreviewTTL = 30 * time.Minute
	// MaxPreviews bounds a store's live preview links.
	MaxPreviews = 20
)

// HomeDraft is the home page being edited: what PageInput carries for
// it, without a slug, status or version.
type HomeDraft struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Sections    []Section `json:"sections"`
}

// Draft is the store's unpublished edits: nil where there are none.
type Draft struct {
	Theme     *ThemeSettings
	Home      *HomeDraft
	UpdatedAt *time.Time
}
