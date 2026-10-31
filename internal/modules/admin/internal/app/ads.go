package app

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/ads"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errNoAds = errs.Unavailable(0, errors.New("admin: ads are not wired"))

func (s *Service) ads(ctx context.Context) (Identity, ads.Console, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Ads == nil {
		return Identity{}, nil, errNoAds
	}
	return who, s.d.Ads, nil
}

// Ads are AdSense's settings and every banner (ADR-0062).
func (s *Service) Ads(ctx context.Context) (ads.Settings, []ads.Banner, error) {
	_, a, err := s.ads(ctx)
	if err != nil {
		return ads.Settings{}, nil, err
	}
	set, err := a.Settings(ctx)
	if err != nil {
		return ads.Settings{}, nil, err
	}
	banners, err := a.Banners(ctx)
	return set, banners, err
}

// AdThumbnail is a banner's image for the console.
func (s *Service) AdThumbnail(b ads.Banner) string {
	if s.d.Ads == nil {
		return ""
	}
	return s.d.Ads.Thumbnail(b)
}

// SaveAdSettings writes AdSense's settings, recorded first.
func (s *Service) SaveAdSettings(ctx context.Context, set ads.Settings, rawReason string) error {
	who, a, err := s.ads(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = set.Validate(); err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "ads.settings_saved", "ads", "settings",
		map[string]any{"reason": reason, "adsense_enabled": set.AdSenseEnabled, "publisher_id": set.PublisherID}); err != nil {
		return err
	}
	return a.SaveSettings(ctx, set)
}

// AdUpload is where the console's browser posts a banner's image.
func (s *Service) AdUpload(ctx context.Context, contentType string) (ads.Upload, error) {
	_, a, err := s.ads(ctx)
	if err != nil {
		return ads.Upload{}, err
	}
	return a.UploadURL(ctx, contentType)
}

// CreateAdBanner adds a banner. It is recorded once saved, with its ID:
// a refused banner leaves nothing to record.
func (s *Service) CreateAdBanner(ctx context.Context, b ads.Banner, uploadKey, rawReason string) (ads.Banner, error) {
	who, a, err := s.ads(ctx)
	if err != nil {
		return ads.Banner{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return ads.Banner{}, err
	}
	if b, err = a.CreateBanner(ctx, b, uploadKey); err != nil {
		return ads.Banner{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "ads.banner_created", "ad_banner", b.ID.String(),
		map[string]any{"reason": reason, "title": b.Title, "placement": string(b.Placement), "link_url": b.Link}); err != nil {
		return ads.Banner{}, err
	}
	return b, nil
}

// SetAdBannerActive shows or stops a banner, recorded first.
func (s *Service) SetAdBannerActive(ctx context.Context, id ulid.ID, active bool, rawReason string) error {
	who, a, err := s.ads(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "ads.banner_active_set", "ad_banner", id.String(),
		map[string]any{"reason": reason, "active": active}); err != nil {
		return err
	}
	return a.SetActive(ctx, id, active)
}
