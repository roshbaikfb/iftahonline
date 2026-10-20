// Package storefront owns the store's look and pages: the theme, the home
// page, landing pages and policies, built from ordered sections
// (ADR-0016). This file is the module's only exported surface (ADR-0001);
// see docs/modules/storefront.md.
package storefront

import "github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"

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
