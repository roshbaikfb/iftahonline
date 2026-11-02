package app

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errNoSite = errs.Unavailable(0, errors.New("admin: the site's content is not wired"))

func (s *Service) site(ctx context.Context) (Identity, site.Console, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Site == nil {
		return Identity{}, nil, errNoSite
	}
	return who, s.d.Site, nil
}

// SiteContent is the platform site's contact details and FAQ (ADR-0073).
func (s *Service) SiteContent(ctx context.Context) (site.Settings, []site.FAQ, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return site.Settings{}, nil, err
	}
	set, err := c.Settings(ctx)
	if err != nil {
		return site.Settings{}, nil, err
	}
	faqs, err := c.FAQs(ctx)
	return set, faqs, err
}

// SaveSiteSettings writes the site's contact details, recorded first.
func (s *Service) SaveSiteSettings(ctx context.Context, set site.Settings, rawReason string) (site.Settings, error) {
	who, c, err := s.site(ctx)
	if err != nil {
		return site.Settings{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return site.Settings{}, err
	}
	// Refused details leave nothing to record.
	set.Normalize()
	if err = set.Validate(); err != nil {
		return site.Settings{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "site.settings_saved", "site", "settings", map[string]any{"reason": reason}); err != nil {
		return site.Settings{}, err
	}
	return c.SaveSettings(ctx, set)
}

// CreateSiteFAQ adds a question. It is recorded once saved, with its ID:
// a refused question leaves nothing to record.
func (s *Service) CreateSiteFAQ(ctx context.Context, f site.FAQ, rawReason string) (site.FAQ, error) {
	who, c, err := s.site(ctx)
	if err != nil {
		return site.FAQ{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return site.FAQ{}, err
	}
	if f, err = c.CreateFAQ(ctx, f); err != nil {
		return site.FAQ{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "site.faq_created", "site_faq", f.ID.String(),
		map[string]any{"reason": reason, "question": f.QuestionAr}); err != nil {
		return site.FAQ{}, err
	}
	return f, nil
}

// UpdateSiteFAQ rewrites a question, recorded first.
func (s *Service) UpdateSiteFAQ(ctx context.Context, f site.FAQ, rawReason string) (site.FAQ, error) {
	who, c, err := s.site(ctx)
	if err != nil {
		return site.FAQ{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return site.FAQ{}, err
	}
	f.Normalize()
	if err = f.Validate(); err != nil {
		return site.FAQ{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "site.faq_updated", "site_faq", f.ID.String(),
		map[string]any{"reason": reason, "question": f.QuestionAr}); err != nil {
		return site.FAQ{}, err
	}
	return c.UpdateFAQ(ctx, f)
}

// DeleteSiteFAQ removes a question, recorded first.
func (s *Service) DeleteSiteFAQ(ctx context.Context, id ulid.ID, rawReason string) error {
	who, c, err := s.site(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "site.faq_deleted", "site_faq", id.String(), map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.DeleteFAQ(ctx, id)
}
