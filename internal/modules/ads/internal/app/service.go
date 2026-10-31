// Package app holds the ads use cases (ADR-0062): what a free store's
// pages show, counting a banner's clicks, /ads.txt, and the console's
// settings and banners.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CacheTTL bounds how long a change of banners or settings takes to reach
// pages (approved: within the caches' lifetime, no mass purge).
const CacheTTL = time.Minute

// DB is a pool: it runs single queries and transactions.
type DB interface {
	postgres.Beginner
	store.Tx
}

// Media is the bucket banners' images are kept in.
type Media struct {
	Objects media.Objects
	Limits  media.Limits
	Signer  *media.Signer // the console's thumbnails
}

// Deps are the service's dependencies.
type Deps struct {
	DB    DB // app_user: the storefront reads and counts
	Admin DB // app_admin: the console writes; nil outside the api role
	Media Media
	IDs   *ulid.Generator
	Log   *slog.Logger
	Now   func() time.Time
	Pick  func(n int) int // the banner draw; nil is random
}

// Service implements the use cases.
type Service struct {
	d   Deps
	mu  sync.Mutex
	hot map[string]live // by country
}

type live struct {
	settings domain.Settings
	banners  []domain.Banner
	until    time.Time
}

// NewService wires the service.
func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Pick == nil {
		d.Pick = func(n int) int { return rand.IntN(n) } //nolint:gosec // which banner shows is not a secret
	}
	return &Service{d: d, hot: map[string]live{}}
}

// liveFor are the settings and banners on show in country, read once a
// minute per country and kept in memory: every free store of the country
// shares them.
func (s *Service) liveFor(ctx context.Context, country string) (live, error) {
	now := s.d.Now()
	s.mu.Lock()
	l, ok := s.hot[country]
	s.mu.Unlock()
	if ok && now.Before(l.until) {
		return l, nil
	}
	settings, err := store.Settings(ctx, s.d.DB)
	if err != nil {
		return live{}, err
	}
	banners, err := store.LiveBanners(ctx, s.d.DB, now, country)
	if err != nil {
		return live{}, err
	}
	l = live{settings: settings, banners: banners, until: now.Add(CacheTTL)}
	s.mu.Lock()
	s.hot[country] = l
	s.mu.Unlock()
	return l, nil
}

// Slots are what each placement of a free store in country shows.
func (s *Service) Slots(ctx context.Context, country string) (map[domain.Placement]domain.Slot, error) {
	l, err := s.liveFor(ctx, country)
	if err != nil {
		return nil, err
	}
	return domain.Choose(l.settings, l.banners, s.d.Pick), nil
}

// PublisherID is AdSense's publisher when it is on, for the pages'
// script; "" otherwise.
func (s *Service) PublisherID(ctx context.Context) (string, error) {
	l, err := s.liveFor(ctx, "")
	if err != nil || !l.settings.AdSenseEnabled {
		return "", err
	}
	return l.settings.PublisherID, nil
}

// AdsTxt is /ads.txt for free stores' hosts ("" with AdSense off).
func (s *Service) AdsTxt(ctx context.Context) (string, error) {
	l, err := s.liveFor(ctx, "")
	if err != nil {
		return "", err
	}
	return l.settings.AdsTxt(), nil
}

// Click returns banner id's link, and counts the click today (UTC) when
// count (the storefront's limits let it); ok is false for a banner not
// on show.
func (s *Service) Click(ctx context.Context, id ulid.ID, count bool) (string, bool, error) {
	now := s.d.Now().UTC()
	link, ok, err := store.LiveLink(ctx, s.d.DB, id, now)
	if err != nil || !ok || !count {
		return link, ok, err
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if err = store.CountClick(ctx, s.d.DB, id, day); err != nil {
		return "", false, err
	}
	return link, true, nil
}

// The console's side, on the admin pool.

var (
	errNoConsole = errs.Unavailable(0, errors.New("ads: the console's pool is not wired"))
	errNoBanner  = errs.NotFound("banner_not_found", "banner not found")
	errImage     = errs.Validation(errs.FieldError{Field: "upload_key", Code: "invalid", Message: "upload the image first"})
)

// uploadPrefix is where the console's browser posts banners' images
// before they are re-encoded.
const uploadPrefix = "platform/ads/uploads/"

// UploadURLTTL is how long a banner image's upload URL works.
const UploadURLTTL = 10 * time.Minute

// Upload is where the console's browser posts a banner's image (an S3
// POST policy, as product images).
type Upload struct {
	Key       string
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
	MaxBytes  int64
}

func (s *Service) admin() (DB, error) {
	if s.d.Admin == nil {
		return nil, errNoConsole
	}
	return s.d.Admin, nil
}

// Settings are AdSense's, for the console.
func (s *Service) Settings(ctx context.Context) (domain.Settings, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Settings{}, err
	}
	return store.Settings(ctx, db)
}

// SaveSettings validates and writes AdSense's settings.
func (s *Service) SaveSettings(ctx context.Context, set domain.Settings) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	if err = set.Validate(); err != nil {
		return err
	}
	return store.SaveSettings(ctx, db, set)
}

// Banners are every banner with its clicks of the last 30 days.
func (s *Service) Banners(ctx context.Context) ([]domain.Banner, error) {
	db, err := s.admin()
	if err != nil {
		return nil, err
	}
	return store.Banners(ctx, db, s.d.Now().UTC().AddDate(0, 0, -30))
}

// UploadURL is a presigned upload of a banner's image, under the
// platform's uploads prefix.
func (s *Service) UploadURL(ctx context.Context, contentType string) (Upload, error) {
	if s.d.Media.Objects == nil {
		return Upload{}, errNoConsole
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return Upload{}, errs.Validation(errs.FieldError{Field: "content_type", Code: "invalid", Message: "JPEG, PNG or WebP (never SVG)"})
	}
	key := uploadPrefix + s.d.IDs.New().String()
	up, err := s.d.Media.Objects.PresignUpload(ctx, key, contentType, domain.MaxImageBytes, UploadURLTTL)
	if err != nil {
		return Upload{}, fmt.Errorf("ads: presign: %w", err)
	}
	return Upload{Key: key, URL: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: domain.MaxImageBytes}, nil
}

// CreateBanner ingests the uploaded image (its real type detected,
// re-encoded, no metadata: CLAUDE.md upload rules) and writes the banner.
func (s *Service) CreateBanner(ctx context.Context, b domain.Banner, uploadKey string) (domain.Banner, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Banner{}, err
	}
	if s.d.Media.Objects == nil {
		return domain.Banner{}, errNoConsole
	}
	if err = b.Validate(); err != nil {
		return domain.Banner{}, err
	}
	if !strings.HasPrefix(uploadKey, uploadPrefix) || len(uploadKey) == len(uploadPrefix) {
		return domain.Banner{}, errImage
	}
	b.ID, b.Active = s.d.IDs.New(), true
	lim := s.d.Media.Limits
	lim.MaxBytes = min(lim.MaxBytes, domain.MaxImageBytes)
	m, key, err := media.Ingest(ctx, s.d.Media.Objects, lim, uploadKey, func(format string) string { return domain.ImageKey(b.ID, format) })
	if code, rejected := media.IsReject(err); rejected {
		return domain.Banner{}, errs.Validation(errs.FieldError{Field: "upload_key", Code: code, Message: "the image was rejected"})
	}
	if err != nil {
		return domain.Banner{}, err
	}
	b.ImageKey, b.Width, b.Height = key, m.Width, m.Height
	if err = postgres.InTx(ctx, db, func(tx pgx.Tx) error { return store.InsertBanner(ctx, tx, b) }); err != nil {
		return domain.Banner{}, err
	}
	// The banner is saved: an upload left behind is only a stray object.
	if err = s.d.Media.Objects.Delete(ctx, uploadKey); err != nil {
		s.d.Log.WarnContext(ctx, "ads: delete a banner's upload", "key", uploadKey, "err", err)
	}
	return b, nil
}

// Thumbnail is a banner's image for the console, signed ("" unwired).
func (s *Service) Thumbnail(b domain.Banner) string {
	if s.d.Media.Signer == nil {
		return ""
	}
	return s.d.Media.Signer.URL(b.ImageKey, 320, "webp")
}

// SetActive turns banner id on or off.
func (s *Service) SetActive(ctx context.Context, id ulid.ID, active bool) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	ok, err := store.SetActive(ctx, db, id, active)
	if err == nil && !ok {
		err = errNoBanner
	}
	return err
}
