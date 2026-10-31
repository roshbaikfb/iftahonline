package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errBannerNotFound = errs.NotFound("banner_not_found", "banner not found")

// AdminGetAds implements StrictServerInterface.
func (h *Handlers) AdminGetAds(ctx context.Context, _ AdminGetAdsRequestObject) (AdminGetAdsResponseObject, error) {
	set, banners, err := h.svc.Ads(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminGetAds200JSONResponse{Settings: toAdSettings(set), Banners: make([]AdminAdBanner, len(banners))}
	for i, b := range banners {
		out.Banners[i] = h.toAdBanner(b)
	}
	return out, nil
}

// AdminSaveAdSettings implements StrictServerInterface.
func (h *Handlers) AdminSaveAdSettings(ctx context.Context, req AdminSaveAdSettingsRequestObject) (AdminSaveAdSettingsResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	set := ads.Settings{AdSenseEnabled: b.AdsenseEnabled, PublisherID: b.PublisherId, Units: map[ads.Placement]string{}}
	for p, u := range map[ads.Placement]*string{ads.HomeTop: b.Units.HomeTop, ads.Grid: b.Units.Grid,
		ads.CategoryTop: b.Units.CategoryTop, ads.ProductBottom: b.Units.ProductBottom} {
		if u != nil && *u != "" {
			set.Units[p] = *u
		}
	}
	if err := h.svc.SaveAdSettings(ctx, set, b.Reason); err != nil {
		return nil, err
	}
	return AdminSaveAdSettings200JSONResponse(toAdSettings(set)), nil
}

// AdminCreateAdUpload implements StrictServerInterface.
func (h *Handlers) AdminCreateAdUpload(ctx context.Context, req AdminCreateAdUploadRequestObject) (AdminCreateAdUploadResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	up, err := h.svc.AdUpload(ctx, string(req.Body.ContentType))
	if err != nil {
		return nil, err
	}
	return AdminCreateAdUpload201JSONResponse{Key: up.Key,
		Upload: PresignedUpload{Url: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: up.MaxBytes}}, nil
}

// AdminCreateAdBanner implements StrictServerInterface.
func (h *Handlers) AdminCreateAdBanner(ctx context.Context, req AdminCreateAdBannerRequestObject) (AdminCreateAdBannerResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	in := req.Body
	b := ads.Banner{Title: in.Title, Link: in.LinkUrl, Placement: ads.Placement(in.Placement), StartsAt: in.StartsAt, Weight: in.Weight}
	if in.Country != nil {
		b.Country = string(*in.Country)
	}
	if in.EndsAt != nil {
		b.EndsAt = *in.EndsAt
	}
	saved, err := h.svc.CreateAdBanner(ctx, b, in.UploadKey, in.Reason)
	if err != nil {
		return nil, err
	}
	return AdminCreateAdBanner201JSONResponse(h.toAdBanner(saved)), nil
}

// AdminSetAdBannerActive implements StrictServerInterface.
func (h *Handlers) AdminSetAdBannerActive(ctx context.Context, req AdminSetAdBannerActiveRequestObject) (AdminSetAdBannerActiveResponseObject, error) {
	id, err := ulid.Parse(req.BannerId)
	if err != nil {
		return nil, errBannerNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.SetAdBannerActive(ctx, id, req.Body.Active, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminSetAdBannerActive204Response{}, nil
}

func toAdSettings(s ads.Settings) AdminAdSettings {
	unit := func(p ads.Placement) *string {
		if u, ok := s.Units[p]; ok {
			return &u
		}
		return nil
	}
	return AdminAdSettings{AdsenseEnabled: s.AdSenseEnabled, PublisherId: s.PublisherID, AdsTxt: s.AdsTxt(),
		Units: AdminAdUnits{HomeTop: unit(ads.HomeTop), Grid: unit(ads.Grid), CategoryTop: unit(ads.CategoryTop), ProductBottom: unit(ads.ProductBottom)}}
}

func (h *Handlers) toAdBanner(b ads.Banner) AdminAdBanner {
	out := AdminAdBanner{Id: b.ID.String(), Title: b.Title, ImageUrl: h.svc.AdThumbnail(b), Width: b.Width, Height: b.Height,
		LinkUrl: b.Link, Placement: AdminAdPlacement(b.Placement), StartsAt: b.StartsAt, Weight: b.Weight, Active: b.Active, Clicks: b.Clicks}
	if b.Country != "" {
		c := AdminAdBannerCountry(b.Country)
		out.Country = &c
	}
	if !b.EndsAt.IsZero() {
		e := b.EndsAt
		out.EndsAt = &e
	}
	return out
}
