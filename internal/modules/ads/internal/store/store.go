// Package store adapts the sqlc queries in adsdb to domain types. Ads are
// platform data (no store, no RLS): the storefront's pool reads them and
// counts clicks, the console's pool writes them.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/store/adsdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is what the functions run on: a pool or a transaction.
type Tx = adsdb.DBTX

// Settings are AdSense's.
func Settings(ctx context.Context, db Tx) (domain.Settings, error) {
	r, err := adsdb.New(db).GetSettings(ctx)
	if err != nil {
		return domain.Settings{}, fmt.Errorf("ads store: settings: %w", err)
	}
	s := domain.Settings{AdSenseEnabled: r.AdsenseEnabled, PublisherID: r.PublisherID, Units: map[domain.Placement]string{}}
	var units map[string]string
	if err = json.Unmarshal(r.Units, &units); err != nil {
		return domain.Settings{}, fmt.Errorf("ads store: units: %w", err)
	}
	for p, u := range units {
		s.Units[domain.Placement(p)] = u
	}
	return s, nil
}

// SaveSettings writes AdSense's settings.
func SaveSettings(ctx context.Context, db Tx, s domain.Settings) error {
	units := make(map[string]string, len(s.Units))
	for p, u := range s.Units {
		if u != "" {
			units[string(p)] = u
		}
	}
	raw, err := json.Marshal(units)
	if err != nil {
		return fmt.Errorf("ads store: units: %w", err)
	}
	if err = adsdb.New(db).SaveSettings(ctx, adsdb.SaveSettingsParams{AdsenseEnabled: s.AdSenseEnabled, PublisherID: s.PublisherID, Units: raw}); err != nil {
		return fmt.Errorf("ads store: save settings: %w", err)
	}
	return nil
}

// LiveBanners are the banners on show at now in country (or every one).
func LiveBanners(ctx context.Context, db Tx, now time.Time, country string) ([]domain.Banner, error) {
	rows, err := adsdb.New(db).LiveBanners(ctx, adsdb.LiveBannersParams{Now: now, Country: &country})
	if err != nil {
		return nil, fmt.Errorf("ads store: live banners: %w", err)
	}
	out := make([]domain.Banner, len(rows))
	for i, r := range rows {
		out[i] = domain.Banner{ID: r.ID, Title: r.Title, ImageKey: r.ImageKey, Width: int(r.Width), Height: int(r.Height), Link: r.LinkUrl,
			Placement: domain.Placement(r.Placement), Weight: int(r.Weight), Active: true}
	}
	return out, nil
}

// LiveLink is banner id's link when it is on show at now; ok is false
// otherwise.
func LiveLink(ctx context.Context, db Tx, id ulid.ID, now time.Time) (link string, ok bool, err error) {
	link, err = adsdb.New(db).GetLiveBanner(ctx, adsdb.GetLiveBannerParams{ID: id, Now: now})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("ads store: banner link: %w", err)
	}
	return link, true, nil
}

// CountClick adds a click to banner id's day.
func CountClick(ctx context.Context, db Tx, id ulid.ID, day time.Time) error {
	if err := adsdb.New(db).CountClick(ctx, adsdb.CountClickParams{BannerID: id, Day: day}); err != nil {
		return fmt.Errorf("ads store: count click: %w", err)
	}
	return nil
}

// Banners are every banner, newest first, with their clicks since since.
func Banners(ctx context.Context, db Tx, since time.Time) ([]domain.Banner, error) {
	rows, err := adsdb.New(db).ListBanners(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("ads store: banners: %w", err)
	}
	out := make([]domain.Banner, len(rows))
	for i, r := range rows {
		b := domain.Banner{ID: r.ID, Title: r.Title, ImageKey: r.ImageKey, Width: int(r.Width), Height: int(r.Height), Link: r.LinkUrl,
			Placement: domain.Placement(r.Placement), Country: r.Country, StartsAt: r.StartsAt, Weight: int(r.Weight), Active: r.Active, Clicks: r.Clicks}
		if r.EndsAt != nil {
			b.EndsAt = *r.EndsAt
		}
		out[i] = b
	}
	return out, nil
}

// InsertBanner writes a new banner.
func InsertBanner(ctx context.Context, db Tx, b domain.Banner) error {
	p := adsdb.InsertBannerParams{ID: b.ID, Title: b.Title, ImageKey: b.ImageKey, Width: int32(b.Width), Height: int32(b.Height), //nolint:gosec // bounded by the image's pixels
		LinkUrl: b.Link, Placement: string(b.Placement), StartsAt: b.StartsAt, Weight: int32(b.Weight)} //nolint:gosec // 1–100
	if b.Country != "" {
		p.Country = &b.Country
	}
	if !b.EndsAt.IsZero() {
		p.EndsAt = &b.EndsAt
	}
	if err := adsdb.New(db).InsertBanner(ctx, p); err != nil {
		return fmt.Errorf("ads store: insert banner: %w", err)
	}
	return nil
}

// SetActive turns banner id on or off; ok is false when there is none.
func SetActive(ctx context.Context, db Tx, id ulid.ID, active bool) (bool, error) {
	n, err := adsdb.New(db).SetBannerActive(ctx, adsdb.SetBannerActiveParams{ID: id, Active: active})
	if err != nil {
		return false, fmt.Errorf("ads store: set active: %w", err)
	}
	return n > 0, nil
}
