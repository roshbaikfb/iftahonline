package app

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The site's guides (S4c), as the console edits them.

var (
	errGuideNotFound = errs.NotFound("guide_not_found", "guide not found")
	errTooManyGuides = errs.Conflict("too_many_guides", "the site holds at most 200 guides")
	errPreviewSize   = errs.Validation(errs.FieldError{Field: "markdown", Code: "invalid", Message: "at most 40000 characters"})
)

func guideItems() items[domain.Guide] {
	return items[domain.Guide]{domain.MaxGuides, errTooManyGuides, errGuideNotFound,
		store.Guides, store.Guide, store.InsertGuide, store.UpdateGuide, store.DeleteGuide,
		func(g, had *domain.Guide, now time.Time) { g.Publish(had.PublishedAt, now.UTC()) }}
}

// Guides are every guide, newest first, without their bodies.
func (s *Service) Guides(ctx context.Context) ([]domain.Guide, error) {
	db, err := s.admin()
	if err != nil {
		return nil, err
	}
	return store.Guides(ctx, db)
}

// Guide is one guide, whole.
func (s *Service) Guide(ctx context.Context, id ulid.ID) (domain.Guide, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Guide{}, err
	}
	g, found, err := store.Guide(ctx, db, id)
	if err != nil {
		return domain.Guide{}, err
	}
	if !found {
		return domain.Guide{}, errGuideNotFound
	}
	return g, nil
}

// CreateGuide adds a guide, with an uploaded cover or none; published,
// it is dated now.
func (s *Service) CreateGuide(ctx context.Context, g domain.Guide, uploadKey string) (domain.Guide, error) {
	return create[domain.Guide, *domain.Guide](ctx, s, guideItems(), g, uploadKey)
}

// UpdateGuide rewrites a guide, its cover as a person's photo; it keeps
// the date it was first published.
func (s *Service) UpdateGuide(ctx context.Context, g domain.Guide, uploadKey string, removeCover bool) (domain.Guide, error) {
	return edit[domain.Guide, *domain.Guide](ctx, s, guideItems(), g, uploadKey, removeCover)
}

// DeleteGuide removes a guide and its cover.
func (s *Service) DeleteGuide(ctx context.Context, id ulid.ID) error {
	return remove[domain.Guide, *domain.Guide](ctx, s, guideItems(), id)
}

// Preview is Markdown as the site would show it.
func (s *Service) Preview(_ context.Context, src string) (string, error) {
	if len([]rune(src)) > 40000 {
		return "", errPreviewSize
	}
	html, err := s.md.HTML(src)
	if err != nil {
		return "", errs.BadRequest("markdown", err)
	}
	return html, nil
}
