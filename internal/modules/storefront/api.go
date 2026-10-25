// Package storefront owns the store's look and pages: the theme, the home
// page, landing pages and policies, built from ordered sections
// (ADR-0016). This file is the module's only exported surface (ADR-0001);
// see docs/modules/storefront.md.
package storefront

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

// Events the module publishes (ADR-0015). Caches of rendered pages
// subscribe to them.
type (
	// ThemeChanged is the storefront.theme.changed event.
	ThemeChanged = domain.ThemeChanged
	// PageChanged is the storefront.page.changed event.
	PageChanged = domain.PageChanged
	// PageKind is home, landing or policy.
	PageKind = domain.PageKind
)

// Event types.
const (
	EventThemeChanged = domain.EventThemeChanged
	EventPageChanged  = domain.EventPageChanged
)

// Page kinds.
const (
	KindHome    = domain.KindHome
	KindLanding = domain.KindLanding
	KindPolicy  = domain.KindPolicy
)

// What the setup wizard's starter template sets (ADR-0034).
type (
	// StarterLook is the theme's look and the home page's sections.
	StarterLook = app.StarterLook
	// Colors is the theme's palette (#rrggbb).
	Colors = domain.Colors
	// Font, Radius and Look are theme choices.
	Font   = domain.Font
	Radius = domain.Radius
	Look   = domain.Look
	// Section is one block of a page; its settings follow its type's
	// schema.
	Section = domain.Section
	// SectionType names a section component.
	SectionType = domain.SectionType
)

// Starter is what the setup wizard's starter template uses. Both methods
// act on the store in ctx and may run twice.
type Starter interface {
	// ApplyLook sets the theme's look and replaces the home page, keeping
	// what the merchant wrote in the theme.
	ApplyLook(ctx context.Context, l StarterLook) error
	// EnsureDraftPolicy saves a draft policy unless the merchant has
	// saved that one.
	EnsureDraftPolicy(ctx context.Context, policy, title, body string) error
}

var _ Starter = (*app.Service)(nil)
