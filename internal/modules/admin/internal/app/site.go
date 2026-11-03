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

// SiteContent is everything the platform site shows from the console
// (ADR-0073).
func (s *Service) SiteContent(ctx context.Context) (site.Content, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return site.Content{}, err
	}
	return c.All(ctx)
}

// SiteImageURL is a site image's signed thumbnail ("" unwired).
func (s *Service) SiteImageURL(key string, size int) string {
	if s.d.Site == nil {
		return ""
	}
	return s.d.Site.ImageURL(key, size, "webp")
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

// SaveSiteStory writes the founding story and the video, recorded first
// (a refused story leaves nothing to record).
func (s *Service) SaveSiteStory(ctx context.Context, ar, en, videoLink, rawReason string) (site.Story, error) {
	who, c, err := s.site(ctx)
	if err != nil {
		return site.Story{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return site.Story{}, err
	}
	id, err := site.ParseVideo(videoLink)
	if err != nil {
		return site.Story{}, err
	}
	st := site.Story{Ar: ar, En: en, VideoID: id}
	st.Normalize()
	if err = st.Validate(); err != nil {
		return site.Story{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "site.story_saved", "site", "story", map[string]any{"reason": reason, "video_id": id}); err != nil {
		return site.Story{}, err
	}
	return c.SaveStory(ctx, ar, en, videoLink)
}

// SiteUpload is where the console's browser posts an image.
func (s *Service) SiteUpload(ctx context.Context, contentType string) (site.Upload, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return site.Upload{}, err
	}
	return c.UploadURL(ctx, contentType)
}

// SetSiteImage makes an upload the logo or the share image (uploadKey ""
// removes it). It is recorded once done: a refused image leaves nothing
// to record.
func (s *Service) SetSiteImage(ctx context.Context, which site.Image, uploadKey, rawReason string) (string, error) {
	who, c, err := s.site(ctx)
	if err != nil {
		return "", err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return "", err
	}
	key, err := c.SetImage(ctx, which, uploadKey)
	if err != nil {
		return "", err
	}
	action := "site.image_set"
	if key == "" {
		action = "site.image_removed"
	}
	if err = s.audit(ctx, &who.AdminID, action, "site_image", string(which), map[string]any{"reason": reason}); err != nil {
		return "", err
	}
	return key, nil
}

// recorded runs a change that saves an entry of the about page, then
// records it with its ID: a refused change leaves nothing to record.
func recorded[T any](ctx context.Context, s *Service, rawReason, action, targetType string,
	save func(site.Console) (T, error), target func(T) (id string, meta map[string]any)) (T, error) {
	var zero T
	who, c, err := s.site(ctx)
	if err != nil {
		return zero, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return zero, err
	}
	v, err := save(c)
	if err != nil {
		return zero, err
	}
	id, meta := target(v)
	meta["reason"] = reason
	if err = s.audit(ctx, &who.AdminID, action, targetType, id, meta); err != nil {
		return zero, err
	}
	return v, nil
}

func personTarget(p site.Person) (string, map[string]any) {
	return p.ID.String(), map[string]any{"name": p.NameAr, "kind": string(p.Kind)}
}

func reviewTarget(t site.Testimonial) (string, map[string]any) {
	return t.ID.String(), map[string]any{"name": t.Name}
}

// CreateSitePerson adds a founder or a team member, recorded once saved.
func (s *Service) CreateSitePerson(ctx context.Context, p site.Person, uploadKey, rawReason string) (site.Person, error) {
	return recorded(ctx, s, rawReason, "site.person_created", "site_person",
		func(c site.Console) (site.Person, error) { return c.CreatePerson(ctx, p, uploadKey) }, personTarget)
}

// UpdateSitePerson rewrites a person, recorded once saved (a new photo
// is ingested by the save itself).
func (s *Service) UpdateSitePerson(ctx context.Context, p site.Person, uploadKey string, removePhoto bool, rawReason string) (site.Person, error) {
	return recorded(ctx, s, rawReason, "site.person_updated", "site_person",
		func(c site.Console) (site.Person, error) { return c.UpdatePerson(ctx, p, uploadKey, removePhoto) }, personTarget)
}

// DeleteSitePerson removes a person, recorded first.
func (s *Service) DeleteSitePerson(ctx context.Context, id ulid.ID, rawReason string) error {
	who, c, err := s.site(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "site.person_deleted", "site_person", id.String(), map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.DeletePerson(ctx, id)
}

// CreateSiteTestimonial adds a merchant's review, recorded once saved.
func (s *Service) CreateSiteTestimonial(ctx context.Context, t site.Testimonial, uploadKey, rawReason string) (site.Testimonial, error) {
	return recorded(ctx, s, rawReason, "site.testimonial_created", "site_testimonial",
		func(c site.Console) (site.Testimonial, error) { return c.CreateTestimonial(ctx, t, uploadKey) }, reviewTarget)
}

// UpdateSiteTestimonial rewrites a review, recorded once saved.
func (s *Service) UpdateSiteTestimonial(ctx context.Context, t site.Testimonial, uploadKey string, removePhoto bool, rawReason string) (site.Testimonial, error) {
	return recorded(ctx, s, rawReason, "site.testimonial_updated", "site_testimonial",
		func(c site.Console) (site.Testimonial, error) {
			return c.UpdateTestimonial(ctx, t, uploadKey, removePhoto)
		}, reviewTarget)
}

// DeleteSiteTestimonial removes a review, recorded first.
func (s *Service) DeleteSiteTestimonial(ctx context.Context, id ulid.ID, rawReason string) error {
	who, c, err := s.site(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "site.testimonial_deleted", "site_testimonial", id.String(), map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.DeleteTestimonial(ctx, id)
}

// SiteGuides are every guide, newest first, without their bodies.
func (s *Service) SiteGuides(ctx context.Context) ([]site.Guide, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return nil, err
	}
	return c.Guides(ctx)
}

// SiteGuide is one guide, whole.
func (s *Service) SiteGuide(ctx context.Context, id ulid.ID) (site.Guide, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return site.Guide{}, err
	}
	return c.Guide(ctx, id)
}

func guideTarget(g site.Guide) (string, map[string]any) {
	return g.ID.String(), map[string]any{"slug": g.Slug, "status": string(g.Status)}
}

// CreateSiteGuide adds a guide, recorded once saved.
func (s *Service) CreateSiteGuide(ctx context.Context, g site.Guide, uploadKey, rawReason string) (site.Guide, error) {
	return recorded(ctx, s, rawReason, "site.guide_created", "site_guide",
		func(c site.Console) (site.Guide, error) { return c.CreateGuide(ctx, g, uploadKey) }, guideTarget)
}

// UpdateSiteGuide rewrites (or publishes) a guide, recorded once saved.
func (s *Service) UpdateSiteGuide(ctx context.Context, g site.Guide, uploadKey string, removeCover bool, rawReason string) (site.Guide, error) {
	return recorded(ctx, s, rawReason, "site.guide_updated", "site_guide",
		func(c site.Console) (site.Guide, error) { return c.UpdateGuide(ctx, g, uploadKey, removeCover) }, guideTarget)
}

// DeleteSiteGuide removes a guide, recorded first.
func (s *Service) DeleteSiteGuide(ctx context.Context, id ulid.ID, rawReason string) error {
	who, c, err := s.site(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "site.guide_deleted", "site_guide", id.String(), map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.DeleteGuide(ctx, id)
}

// PreviewGuide is Markdown as the site would show it; nothing is saved
// or recorded.
func (s *Service) PreviewGuide(ctx context.Context, src string) (string, error) {
	_, c, err := s.site(ctx)
	if err != nil {
		return "", err
	}
	return c.Preview(ctx, src)
}
