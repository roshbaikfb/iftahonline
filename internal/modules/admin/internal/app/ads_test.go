package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeAds is the ads module's console side, in memory.
type fakeAds struct {
	mu       sync.Mutex
	settings ads.Settings
	banners  []ads.Banner
}

func (f *fakeAds) Settings(context.Context) (ads.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeAds) SaveSettings(_ context.Context, s ads.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = s
	return nil
}

func (f *fakeAds) Banners(context.Context) ([]ads.Banner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ads.Banner(nil), f.banners...), nil
}

func (f *fakeAds) UploadURL(_ context.Context, contentType string) (ads.Upload, error) {
	return ads.Upload{Key: "platform/ads/uploads/x", URL: "https://s3.test", Fields: map[string]string{"Content-Type": contentType},
		ExpiresAt: time.Now().Add(time.Minute), MaxBytes: 1 << 20}, nil
}

func (f *fakeAds) CreateBanner(_ context.Context, b ads.Banner, key string) (ads.Banner, error) {
	if err := b.Validate(); err != nil {
		return ads.Banner{}, err
	}
	if key == "" {
		return ads.Banner{}, errs.Validation(errs.FieldError{Field: "upload_key", Code: "invalid"})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b.ID, b.Active, b.ImageKey = ulid.NewGenerator().New(), true, "platform/ads/x.png"
	f.banners = append(f.banners, b)
	return b, nil
}

func (f *fakeAds) SetActive(_ context.Context, id ulid.ID, active bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.banners {
		if f.banners[i].ID == id {
			f.banners[i].Active = active
			return nil
		}
	}
	return errs.NotFound("banner_not_found", "banner not found")
}

func (f *fakeAds) Thumbnail(b ads.Banner) string { return "https://img.test/" + b.ImageKey }

// TestAdsFromTheConsole: AdSense's settings and banners are managed with a
// reason, and every change is in the admin log (ADR-0062).
func TestAdsFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	on := ads.Settings{AdSenseEnabled: true, PublisherID: "ca-pub-1234567890123456", Units: map[ads.Placement]string{ads.Grid: "1234567890"}}

	if err := f.svc.SaveAdSettings(ctx, on, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if err := f.svc.SaveAdSettings(ctx, ads.Settings{AdSenseEnabled: true}, "link AdSense"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no publisher: %v", err)
	}
	if err := f.svc.SaveAdSettings(ctx, on, "link AdSense"); err != nil {
		t.Fatal(err)
	}
	set, banners, err := f.svc.Ads(ctx)
	if err != nil || set.PublisherID != on.PublisherID || len(banners) != 0 {
		t.Fatalf("ads = %+v %v, %v", set, banners, err)
	}
	if up, upErr := f.svc.AdUpload(ctx, "image/png"); upErr != nil || up.Key == "" {
		t.Fatalf("upload = %+v, %v", up, upErr)
	}

	b := ads.Banner{Title: "افتح متجرك", Link: "https://iftahonline.com/start", Placement: ads.HomeTop, StartsAt: time.Now(), Weight: 10}
	if _, err = f.svc.CreateAdBanner(ctx, b, "platform/ads/uploads/x", ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err = f.svc.CreateAdBanner(ctx, b, "", "November"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no image: %v", err)
	}
	saved, err := f.svc.CreateAdBanner(ctx, b, "platform/ads/uploads/x", "November campaign")
	if err != nil || !saved.Active {
		t.Fatalf("create = %+v, %v", saved, err)
	}
	if f.svc.AdThumbnail(saved) != "https://img.test/platform/ads/x.png" || f.cli.AdThumbnail(saved) != "" {
		t.Fatal("thumbnail")
	}
	if err = f.svc.SetAdBannerActive(ctx, saved.ID, false, "no"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("short reason: %v", err)
	}
	if err = f.svc.SetAdBannerActive(ctx, saved.ID, false, "campaign over"); err != nil {
		t.Fatal(err)
	}
	if _, banners, _ = f.svc.Ads(ctx); len(banners) != 1 || banners[0].Active {
		t.Fatalf("banners = %+v", banners)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && strings.HasPrefix(e.Action, "ads.") {
			got = append(got, e.Action+":"+e.TargetID)
		}
	}
	want := "ads.banner_active_set:" + saved.ID.String() + ",ads.banner_created:" + saved.ID.String() + ",ads.settings_saved:settings"
	if strings.Join(got, ",") != want {
		t.Fatalf("admin log = %v", got)
	}

	if _, _, err = f.svc.Ads(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	for name, call := range map[string]func() error{
		"ads":      func() error { _, _, cerr := f.cli.Ads(ctx); return cerr },
		"settings": func() error { return f.cli.SaveAdSettings(ctx, on, "reason") },
		"upload":   func() error { _, cerr := f.cli.AdUpload(ctx, "image/png"); return cerr },
		"create":   func() error { _, cerr := f.cli.CreateAdBanner(ctx, b, "k", "reason"); return cerr },
		"active":   func() error { return f.cli.SetAdBannerActive(ctx, saved.ID, true, "reason") },
	} {
		if err = call(); !errs.Is(err, errs.KindUnavailable) {
			t.Fatalf("cli %s: %v", name, err)
		}
	}
}
