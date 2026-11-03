package httpapi

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errGuideNotFound = errs.NotFound("guide_not_found", "guide not found")

// AdminListSiteGuides implements StrictServerInterface.
func (h *Handlers) AdminListSiteGuides(ctx context.Context, _ AdminListSiteGuidesRequestObject) (AdminListSiteGuidesResponseObject, error) {
	list, err := h.svc.SiteGuides(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListSiteGuides200JSONResponse{Items: make([]AdminSiteGuideSummary, len(list))}
	for i, g := range list {
		out.Items[i] = AdminSiteGuideSummary{Id: g.ID.String(), Slug: g.Slug, Status: AdminSiteGuideSummaryStatus(g.Status), TitleAr: g.TitleAr,
			SummaryAr: g.SummaryAr, HasEn: g.HasEnglish(), CoverUrl: h.thumb(g.CoverKey), PublishedAt: optTime(g), UpdatedAt: g.UpdatedAt}
	}
	return out, nil
}

// AdminGetSiteGuide implements StrictServerInterface.
func (h *Handlers) AdminGetSiteGuide(ctx context.Context, req AdminGetSiteGuideRequestObject) (AdminGetSiteGuideResponseObject, error) {
	id, err := ulid.Parse(req.GuideId)
	if err != nil {
		return nil, errGuideNotFound
	}
	g, err := h.svc.SiteGuide(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetSiteGuide200JSONResponse(h.toSiteGuide(g)), nil
}

// AdminCreateSiteGuide implements StrictServerInterface.
func (h *Handlers) AdminCreateSiteGuide(ctx context.Context, req AdminCreateSiteGuideRequestObject) (AdminCreateSiteGuideResponseObject, error) {
	g, err := withBody(req.Body, func(b AdminSiteGuideInput) (site.Guide, error) {
		return h.svc.CreateSiteGuide(ctx, fromSiteGuide(ulid.ID{}, b), str(b.UploadKey), b.Reason)
	})
	if err != nil {
		return nil, err
	}
	return AdminCreateSiteGuide201JSONResponse(h.toSiteGuide(g)), nil
}

// AdminUpdateSiteGuide implements StrictServerInterface.
func (h *Handlers) AdminUpdateSiteGuide(ctx context.Context, req AdminUpdateSiteGuideRequestObject) (AdminUpdateSiteGuideResponseObject, error) {
	g, err := updateEntry(req.GuideId, errGuideNotFound, req.Body, func(id ulid.ID, b AdminSiteGuideInput) (site.Guide, error) {
		return h.svc.UpdateSiteGuide(ctx, fromSiteGuide(id, b), str(b.UploadKey), flag(b.RemoveCover), b.Reason)
	})
	if err != nil {
		return nil, err
	}
	return AdminUpdateSiteGuide200JSONResponse(h.toSiteGuide(g)), nil
}

// AdminDeleteSiteGuide implements StrictServerInterface.
func (h *Handlers) AdminDeleteSiteGuide(ctx context.Context, req AdminDeleteSiteGuideRequestObject) (AdminDeleteSiteGuideResponseObject, error) {
	if err := deleteEntry(req.GuideId, errGuideNotFound, func(id ulid.ID) error {
		return h.svc.DeleteSiteGuide(ctx, id, req.Params.Reason)
	}); err != nil {
		return nil, err
	}
	return AdminDeleteSiteGuide204Response{}, nil
}

// AdminPreviewSiteGuide implements StrictServerInterface.
func (h *Handlers) AdminPreviewSiteGuide(ctx context.Context, req AdminPreviewSiteGuideRequestObject) (AdminPreviewSiteGuideResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	html, err := h.svc.PreviewGuide(ctx, req.Body.Markdown)
	if err != nil {
		return nil, err
	}
	return AdminPreviewSiteGuide200JSONResponse{Html: html}, nil
}

func fromSiteGuide(id ulid.ID, in AdminSiteGuideInput) site.Guide {
	return site.Guide{ID: id, Slug: in.Slug, Status: site.GuideStatus(in.Status), TitleAr: in.TitleAr, SummaryAr: in.SummaryAr, BodyAr: in.BodyAr,
		TitleEn: str(in.TitleEn), SummaryEn: str(in.SummaryEn), BodyEn: str(in.BodyEn)}
}

func (h *Handlers) toSiteGuide(g site.Guide) AdminSiteGuide {
	return AdminSiteGuide{Id: g.ID.String(), Slug: g.Slug, Status: AdminSiteGuideStatus(g.Status), TitleAr: g.TitleAr, SummaryAr: g.SummaryAr,
		BodyAr: g.BodyAr, TitleEn: g.TitleEn, SummaryEn: g.SummaryEn, BodyEn: g.BodyEn, CoverUrl: h.thumb(g.CoverKey), PublishedAt: optTime(g),
		UpdatedAt: g.UpdatedAt}
}

// optTime is a guide's first publishing, absent before it.
func optTime(g site.Guide) *time.Time {
	if g.PublishedAt.IsZero() {
		return nil
	}
	t := g.PublishedAt
	return &t
}
