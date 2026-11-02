package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errFAQNotFound = errs.NotFound("faq_not_found", "question not found")

// AdminGetSite implements StrictServerInterface.
func (h *Handlers) AdminGetSite(ctx context.Context, _ AdminGetSiteRequestObject) (AdminGetSiteResponseObject, error) {
	set, faqs, err := h.svc.SiteContent(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminGetSite200JSONResponse{Settings: toSiteSettings(set), Faqs: make([]AdminSiteFAQ, len(faqs))}
	for i, f := range faqs {
		out.Faqs[i] = toSiteFAQ(f)
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
