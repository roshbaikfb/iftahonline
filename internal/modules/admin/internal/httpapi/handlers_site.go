package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errFAQNotFound    = errs.NotFound("faq_not_found", "question not found")
	errPersonNotFound = errs.NotFound("person_not_found", "person not found")
	errReviewNotFound = errs.NotFound("testimonial_not_found", "review not found")
)

// thumbSize is the console's thumbnails' side.
const thumbSize = 320

func (h *Handlers) thumb(key string) string { return h.svc.SiteImageURL(key, thumbSize) }

// AdminGetSite implements StrictServerInterface.
func (h *Handlers) AdminGetSite(ctx context.Context, _ AdminGetSiteRequestObject) (AdminGetSiteResponseObject, error) {
	c, err := h.svc.SiteContent(ctx)
	if err != nil {
		return nil, err
	}
	st := c.Identity.Story
	out := AdminGetSite200JSONResponse{Settings: toSiteSettings(c.Settings), Faqs: make([]AdminSiteFAQ, len(c.FAQs)),
		Story:  AdminSiteStory{StoryAr: st.Ar, StoryEn: st.En, VideoId: st.VideoID},
		Images: AdminSiteImages{LogoUrl: h.thumb(c.Identity.LogoKey), ShareUrl: h.thumb(c.Identity.ShareKey)},
		People: make([]AdminSitePerson, len(c.People)), Testimonials: make([]AdminSiteTestimonial, len(c.Testimonials))}
	for i, f := range c.FAQs {
		out.Faqs[i] = toSiteFAQ(f)
	}
	for i, p := range c.People {
		out.People[i] = h.toSitePerson(p)
	}
	for i, t := range c.Testimonials {
		out.Testimonials[i] = h.toSiteTestimonial(t)
	}
	return out, nil
}

// AdminSaveSiteSettings implements StrictServerInterface.
func (h *Handlers) AdminSaveSiteSettings(ctx context.Context, req AdminSaveSiteSettingsRequestObject) (AdminSaveSiteSettingsResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	set := site.Settings{WhatsApp: b.Whatsapp, WhatsAppIntl: b.WhatsappIntl, Email: b.Email, CompanyNumber: b.CompanyNumber,
		Socials: map[site.Social]string{}}
	for k, v := range socialFields(&b.Socials) {
		if *v != nil {
			set.Socials[k] = **v
		}
	}
	saved, err := h.svc.SaveSiteSettings(ctx, set, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSaveSiteSettings200JSONResponse(toSiteSettings(saved)), nil
}

// AdminCreateSiteFAQ implements StrictServerInterface.
func (h *Handlers) AdminCreateSiteFAQ(ctx context.Context, req AdminCreateSiteFAQRequestObject) (AdminCreateSiteFAQResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	f, err := h.svc.CreateSiteFAQ(ctx, fromSiteFAQ(ulid.ID{}, *req.Body), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminCreateSiteFAQ201JSONResponse(toSiteFAQ(f)), nil
}

// AdminUpdateSiteFAQ implements StrictServerInterface.
func (h *Handlers) AdminUpdateSiteFAQ(ctx context.Context, req AdminUpdateSiteFAQRequestObject) (AdminUpdateSiteFAQResponseObject, error) {
	id, err := ulid.Parse(req.FaqId)
	if err != nil {
		return nil, errFAQNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	f, err := h.svc.UpdateSiteFAQ(ctx, fromSiteFAQ(id, *req.Body), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminUpdateSiteFAQ200JSONResponse(toSiteFAQ(f)), nil
}

// AdminDeleteSiteFAQ implements StrictServerInterface.
func (h *Handlers) AdminDeleteSiteFAQ(ctx context.Context, req AdminDeleteSiteFAQRequestObject) (AdminDeleteSiteFAQResponseObject, error) {
	id, err := ulid.Parse(req.FaqId)
	if err != nil {
		return nil, errFAQNotFound
	}
	if err = h.svc.DeleteSiteFAQ(ctx, id, req.Params.Reason); err != nil {
		return nil, err
	}
	return AdminDeleteSiteFAQ204Response{}, nil
}

// socialFields are the contract's field for each network.
func socialFields(s *AdminSiteSocials) map[site.Social]**string {
	return map[site.Social]**string{site.Facebook: &s.Facebook, site.Instagram: &s.Instagram, site.TikTok: &s.Tiktok,
		site.LinkedIn: &s.Linkedin, site.YouTube: &s.Youtube, site.X: &s.X}
}

func toSiteSettings(s site.Settings) AdminSiteSettings {
	out := AdminSiteSettings{Whatsapp: s.WhatsApp, WhatsappIntl: s.WhatsAppIntl, Email: s.Email, CompanyNumber: s.CompanyNumber}
	for k, v := range socialFields(&out.Socials) {
		if link, ok := s.Socials[k]; ok {
			*v = &link
		}
	}
	return out
}

func fromSiteFAQ(id ulid.ID, in AdminSiteFAQInput) site.FAQ {
	f := site.FAQ{ID: id, Position: in.Position, OnHome: in.OnHome, QuestionAr: in.QuestionAr, AnswerAr: in.AnswerAr}
	if in.QuestionEn != nil {
		f.QuestionEn = *in.QuestionEn
	}
	if in.AnswerEn != nil {
		f.AnswerEn = *in.AnswerEn
	}
	return f
}

func toSiteFAQ(f site.FAQ) AdminSiteFAQ {
	return AdminSiteFAQ{Id: f.ID.String(), Position: f.Position, OnHome: f.OnHome, QuestionAr: f.QuestionAr, AnswerAr: f.AnswerAr,
		QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn}
}

// AdminSaveSiteStory implements StrictServerInterface.
func (h *Handlers) AdminSaveSiteStory(ctx context.Context, req AdminSaveSiteStoryRequestObject) (AdminSaveSiteStoryResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	st, err := h.svc.SaveSiteStory(ctx, b.StoryAr, b.StoryEn, b.VideoUrl, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSaveSiteStory200JSONResponse{StoryAr: st.Ar, StoryEn: st.En, VideoId: st.VideoID}, nil
}

// AdminCreateSiteUpload implements StrictServerInterface.
func (h *Handlers) AdminCreateSiteUpload(ctx context.Context, req AdminCreateSiteUploadRequestObject) (AdminCreateSiteUploadResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	up, err := h.svc.SiteUpload(ctx, string(req.Body.ContentType))
	if err != nil {
		return nil, err
	}
	return AdminCreateSiteUpload201JSONResponse{Key: up.Key,
		Upload: PresignedUpload{Url: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: up.MaxBytes}}, nil
}

// AdminSetSiteImage implements StrictServerInterface.
func (h *Handlers) AdminSetSiteImage(ctx context.Context, req AdminSetSiteImageRequestObject) (AdminSetSiteImageResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	key, err := h.svc.SetSiteImage(ctx, site.Image(req.Image), req.Body.UploadKey, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSetSiteImage200JSONResponse{Url: h.thumb(key)}, nil
}

// AdminRemoveSiteImage implements StrictServerInterface.
func (h *Handlers) AdminRemoveSiteImage(ctx context.Context, req AdminRemoveSiteImageRequestObject) (AdminRemoveSiteImageResponseObject, error) {
	if _, err := h.svc.SetSiteImage(ctx, site.Image(req.Image), "", req.Params.Reason); err != nil {
		return nil, err
	}
	return AdminRemoveSiteImage204Response{}, nil
}

// AdminCreateSitePerson implements StrictServerInterface.
func (h *Handlers) AdminCreateSitePerson(ctx context.Context, req AdminCreateSitePersonRequestObject) (AdminCreateSitePersonResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	p, err := h.svc.CreateSitePerson(ctx, fromSitePerson(ulid.ID{}, *req.Body), str(req.Body.UploadKey), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminCreateSitePerson201JSONResponse(h.toSitePerson(p)), nil
}

// AdminUpdateSitePerson implements StrictServerInterface.
func (h *Handlers) AdminUpdateSitePerson(ctx context.Context, req AdminUpdateSitePersonRequestObject) (AdminUpdateSitePersonResponseObject, error) {
	id, err := ulid.Parse(req.PersonId)
	if err != nil {
		return nil, errPersonNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	p, err := h.svc.UpdateSitePerson(ctx, fromSitePerson(id, *b), str(b.UploadKey), b.RemovePhoto != nil && *b.RemovePhoto, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminUpdateSitePerson200JSONResponse(h.toSitePerson(p)), nil
}

// AdminDeleteSitePerson implements StrictServerInterface.
func (h *Handlers) AdminDeleteSitePerson(ctx context.Context, req AdminDeleteSitePersonRequestObject) (AdminDeleteSitePersonResponseObject, error) {
	id, err := ulid.Parse(req.PersonId)
	if err != nil {
		return nil, errPersonNotFound
	}
	if err = h.svc.DeleteSitePerson(ctx, id, req.Params.Reason); err != nil {
		return nil, err
	}
	return AdminDeleteSitePerson204Response{}, nil
}

// AdminCreateSiteTestimonial implements StrictServerInterface.
func (h *Handlers) AdminCreateSiteTestimonial(ctx context.Context, req AdminCreateSiteTestimonialRequestObject) (AdminCreateSiteTestimonialResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	t, err := h.svc.CreateSiteTestimonial(ctx, fromSiteTestimonial(ulid.ID{}, *req.Body), str(req.Body.UploadKey), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminCreateSiteTestimonial201JSONResponse(h.toSiteTestimonial(t)), nil
}

// AdminUpdateSiteTestimonial implements StrictServerInterface.
func (h *Handlers) AdminUpdateSiteTestimonial(ctx context.Context, req AdminUpdateSiteTestimonialRequestObject) (AdminUpdateSiteTestimonialResponseObject, error) {
	id, err := ulid.Parse(req.TestimonialId)
	if err != nil {
		return nil, errReviewNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	t, err := h.svc.UpdateSiteTestimonial(ctx, fromSiteTestimonial(id, *b), str(b.UploadKey), b.RemovePhoto != nil && *b.RemovePhoto, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminUpdateSiteTestimonial200JSONResponse(h.toSiteTestimonial(t)), nil
}

// AdminDeleteSiteTestimonial implements StrictServerInterface.
func (h *Handlers) AdminDeleteSiteTestimonial(ctx context.Context, req AdminDeleteSiteTestimonialRequestObject) (AdminDeleteSiteTestimonialResponseObject, error) {
	id, err := ulid.Parse(req.TestimonialId)
	if err != nil {
		return nil, errReviewNotFound
	}
	if err = h.svc.DeleteSiteTestimonial(ctx, id, req.Params.Reason); err != nil {
		return nil, err
	}
	return AdminDeleteSiteTestimonial204Response{}, nil
}

// str is an optional text, "" when absent.
func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fromSitePerson(id ulid.ID, in AdminSitePersonInput) site.Person {
	return site.Person{ID: id, Kind: site.PersonKind(in.Kind), Position: in.Position, NameAr: in.NameAr, NameEn: str(in.NameEn),
		RoleAr: in.RoleAr, RoleEn: str(in.RoleEn), BioAr: str(in.BioAr), BioEn: str(in.BioEn), LinkedIn: str(in.LinkedinUrl), X: str(in.XUrl)}
}

func (h *Handlers) toSitePerson(p site.Person) AdminSitePerson {
	return AdminSitePerson{Id: p.ID.String(), Kind: AdminSitePersonKind(p.Kind), Position: p.Position, NameAr: p.NameAr, NameEn: p.NameEn,
		RoleAr: p.RoleAr, RoleEn: p.RoleEn, BioAr: p.BioAr, BioEn: p.BioEn, PhotoUrl: h.thumb(p.PhotoKey), LinkedinUrl: p.LinkedIn, XUrl: p.X}
}

func fromSiteTestimonial(id ulid.ID, in AdminSiteTestimonialInput) site.Testimonial {
	return site.Testimonial{ID: id, Position: in.Position, Name: in.Name, StoreName: str(in.StoreName), StoreURL: str(in.StoreUrl),
		QuoteAr: in.QuoteAr, QuoteEn: str(in.QuoteEn)}
}

func (h *Handlers) toSiteTestimonial(t site.Testimonial) AdminSiteTestimonial {
	return AdminSiteTestimonial{Id: t.ID.String(), Position: t.Position, Name: t.Name, StoreName: t.StoreName, StoreUrl: t.StoreURL,
		QuoteAr: t.QuoteAr, QuoteEn: t.QuoteEn, PhotoUrl: h.thumb(t.PhotoKey)}
}
