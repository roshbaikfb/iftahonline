package app

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

// StarterLook is what the setup wizard's starter template sets
// (ADR-0034): the theme's look, and the home page's sections.
type StarterLook struct {
	Colors       domain.Colors
	Font         domain.Font
	Radius       domain.Radius
	Look         domain.Look
	Announcement string // used only when the merchant has written none
	Home         []domain.Section
}

// ApplyLook sets the theme's colours, font, radius and look, and replaces
// the home page. What the merchant wrote (announcement, about, contact,
// low-stock threshold) is kept; a template announcement only fills an
// empty one. Running it twice gives the same result.
func (s *Service) ApplyLook(ctx context.Context, l StarterLook) error {
	t, err := s.GetTheme(ctx)
	if err != nil {
		return err
	}
	in := t.Settings
	in.Colors, in.Font, in.Radius, in.Look = l.Colors, l.Font, l.Radius, l.Look
	if in.Announcement == "" {
		in.Announcement = l.Announcement
	}
	if _, err = s.UpdateTheme(ctx, in, t.Version); err != nil {
		return err
	}
	home, err := s.GetHome(ctx)
	if err != nil {
		return err
	}
	_, err = s.SaveHome(ctx, domain.PageInput{Title: home.Title, Description: home.Description, Sections: l.Home, Version: home.Version})
	return err
}

// EnsureDraftPolicy saves a policy as a draft for the merchant to review,
// unless they have saved that policy already.
func (s *Service) EnsureDraftPolicy(ctx context.Context, policy, title, body string) error {
	cur, err := s.GetPolicy(ctx, policy)
	if err != nil || cur.Version > 0 {
		return err
	}
	_, err = s.SavePolicy(ctx, policy, domain.PageInput{Title: title, Body: body, Status: string(domain.StatusDraft)})
	return err
}
