package app_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// bucket is an in-memory media.Objects.
type bucket struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (b *bucket) PresignUpload(_ context.Context, key, _ string, maxBytes int64, ttl time.Duration) (media.Upload, error) {
	return media.Upload{URL: "https://s3.test/media", Fields: map[string]string{"key": key}, ExpiresAt: time.Now().Add(ttl), MaxBytes: maxBytes}, nil
}

func (b *bucket) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.objects[key]
	if !ok {
		return nil, media.ErrNoObject
	}
	return d, nil
}

func (b *bucket) Put(_ context.Context, key, _ string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = data
	return nil
}

func (b *bucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *bucket) keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(maps.Keys(b.objects))
}

func pngImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 200))
	for x := range 640 {
		img.Set(x, x%200, color.RGBA{R: 15, G: 46, B: 82, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func open(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestAdsEndToEnd: the console sets AdSense up and adds banners; free
// stores' slots show the banners first, then AdSense, change within the
// cache's minute, and clicks are counted per day. One test: the ads are
// the platform's, one set for the package's database.
func TestAdsEndToEnd(t *testing.T) {
	ctx := t.Context()
	appPool, adminPool := open(t, infra.AppURL), open(t, infra.AdminURL)
	clk := &clock{now: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	b := &bucket{objects: map[string][]byte{}}
	deps := app.Deps{DB: appPool, Admin: adminPool, IDs: ulid.NewGenerator(), Now: clk.Now, Pick: func(int) int { return 0 },
		Media: app.Media{Objects: b, Limits: media.Limits{MaxBytes: 10 << 20, MaxPixels: 40_000_000}}}
	svc := app.NewService(deps)
	shopOnly := app.NewService(app.Deps{DB: appPool, IDs: deps.IDs, Now: clk.Now})

	// Nothing set up: every slot is empty, /ads.txt is too.
	slots, err := svc.Slots(ctx, "EG")
	if err != nil {
		t.Fatal(err)
	}
	for p, sl := range slots {
		if !sl.Empty() {
			t.Fatalf("%s = %+v before any ad", p, sl)
		}
	}
	if txt, txtErr := svc.AdsTxt(ctx); txtErr != nil || txt != "" {
		t.Fatalf("ads.txt = %q, %v", txt, txtErr)
	}

	// The console's pool is the only writer.
	if err = shopOnly.SaveSettings(ctx, domain.Settings{}); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("shop pool saves: %v", err)
	}
	if _, err = appPool.Exec(ctx, "UPDATE ad_settings SET adsense_enabled = false"); err == nil {
		t.Fatal("app_user wrote ad_settings")
	}
	if err = svc.SaveSettings(ctx, domain.Settings{AdSenseEnabled: true}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no publisher: %v", err)
	}
	on := domain.Settings{AdSenseEnabled: true, PublisherID: "ca-pub-1234567890123456",
		Units: map[domain.Placement]string{domain.HomeTop: "1111111111", domain.Grid: "2222222222"}}
	if err = svc.SaveSettings(ctx, on); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Settings(ctx)
	if err != nil || !got.AdSenseEnabled || got.PublisherID != on.PublisherID || !maps.Equal(got.Units, on.Units) {
		t.Fatalf("settings = %+v, %v", got, err)
	}

	// Cached a minute: the pages see the change after it.
	if slots, _ = svc.Slots(ctx, "EG"); slots[domain.HomeTop].Unit != "" {
		t.Fatalf("change seen before the cache's minute: %+v", slots[domain.HomeTop])
	}
	clk.add(app.CacheTTL)
	if slots, _ = svc.Slots(ctx, "EG"); slots[domain.HomeTop].Unit != "1111111111" || slots[domain.CategoryTop].Unit != "" {
		t.Fatalf("after a minute = %+v", slots)
	}
	if pub, pubErr := svc.PublisherID(ctx); pubErr != nil || pub != on.PublisherID {
		t.Fatalf("publisher = %q, %v", pub, pubErr)
	}
	if txt, _ := svc.AdsTxt(ctx); !strings.HasPrefix(txt, "google.com, pub-1234567890123456, DIRECT") {
		t.Fatalf("ads.txt = %q", txt)
	}

	// A banner: an upload, ingested and re-encoded, under the platform's space.
	if _, err = svc.UploadURL(ctx, "image/svg+xml"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("svg upload: %v", err)
	}
	up, err := svc.UploadURL(ctx, "image/png")
	if err != nil || !strings.HasPrefix(up.Key, "platform/ads/uploads/") || up.MaxBytes != domain.MaxImageBytes || up.Fields["key"] != up.Key {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	banner := domain.Banner{Title: "افتح متجرك", Link: "https://iftahonline.com/start", Placement: domain.HomeTop, Country: "EG",
		StartsAt: clk.Now().Add(-time.Hour), Weight: 10}
	if _, err = svc.CreateBanner(ctx, banner, up.Key); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("nothing uploaded: %v", err)
	}
	if _, err = svc.CreateBanner(ctx, banner, "stores/x/uploads/y"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a store's upload: %v", err)
	}
	if err = b.Put(ctx, up.Key, "image/png", []byte("<svg/>")); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateBanner(ctx, banner, up.Key); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("not an image: %v", err)
	}
	if err = b.Put(ctx, up.Key, "image/png", pngImage(t)); err != nil {
		t.Fatal(err)
	}
	bad := banner
	bad.Link = "http://x.com"
	if _, err = svc.CreateBanner(ctx, bad, up.Key); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("http link: %v", err)
	}
	saved, err := svc.CreateBanner(ctx, banner, up.Key)
	if err != nil || !saved.Active || saved.Width != 640 || saved.Height != 200 || !strings.HasPrefix(saved.ImageKey, "platform/ads/"+saved.ID.String()+".") {
		t.Fatalf("banner = %+v, %v", saved, err)
	}
	if keys := b.keys(); len(keys) != 1 || keys[0] != saved.ImageKey {
		t.Fatalf("bucket = %v (the upload is deleted)", keys)
	}
	if svc.Thumbnail(saved) != "" {
		t.Fatal("thumbnail without a signer")
	}

	// Banners first; Egypt's banner is not Saudi Arabia's.
	clk.add(app.CacheTTL)
	if slots, _ = svc.Slots(ctx, "EG"); slots[domain.HomeTop].Banner == nil || slots[domain.HomeTop].Banner.ID != saved.ID || slots[domain.HomeTop].Unit != "" {
		t.Fatalf("EG home_top = %+v", slots[domain.HomeTop])
	}
	if slots, _ = svc.Slots(ctx, "SA"); slots[domain.HomeTop].Banner != nil || slots[domain.HomeTop].Unit != "1111111111" {
		t.Fatalf("SA home_top = %+v", slots[domain.HomeTop])
	}

	// Clicks: counted per day, from the shop's pool.
	for range 2 {
		if link, ok, clickErr := shopOnly.Click(ctx, saved.ID, true); clickErr != nil || !ok || link != banner.Link {
			t.Fatalf("click = %q, %v, %v", link, ok, clickErr)
		}
	}
	clk.add(24 * time.Hour)
	if _, _, err = shopOnly.Click(ctx, saved.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, ok, clickErr := shopOnly.Click(ctx, deps.IDs.New(), true); clickErr != nil || ok {
		t.Fatalf("unknown banner click = %v, %v", ok, clickErr)
	}
	if link, ok, clickErr := shopOnly.Click(ctx, saved.ID, false); clickErr != nil || !ok || link != banner.Link {
		t.Fatalf("an uncounted click = %q, %v, %v", link, ok, clickErr)
	}
	var days int
	if err = adminPool.QueryRow(ctx, "SELECT count(*) FROM ad_clicks WHERE banner_id = $1", saved.ID).Scan(&days); err != nil || days != 2 {
		t.Fatalf("click days = %d, %v", days, err)
	}
	list, err := svc.Banners(ctx)
	if err != nil || len(list) != 1 || list[0].Clicks != 3 || list[0].Country != "EG" || !list[0].EndsAt.IsZero() {
		t.Fatalf("banners = %+v, %v", list, err)
	}

	// Stopped: no longer shown nor clicked through.
	if err = svc.SetActive(ctx, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetActive(ctx, deps.IDs.New(), false); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown banner: %v", err)
	}
	if _, ok, _ := shopOnly.Click(ctx, saved.ID, true); ok {
		t.Fatal("a stopped banner clicked through")
	}
	clk.add(app.CacheTTL)
	if slots, _ = svc.Slots(ctx, "EG"); slots[domain.HomeTop].Banner != nil {
		t.Fatalf("stopped banner shown: %+v", slots[domain.HomeTop])
	}

	// The console's other calls need its pool too.
	if _, err = shopOnly.Settings(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("settings: %v", err)
	}
	if _, err = shopOnly.Banners(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("banners: %v", err)
	}
	if _, err = shopOnly.UploadURL(ctx, "image/png"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("upload: %v", err)
	}
	if _, err = shopOnly.CreateBanner(ctx, banner, up.Key); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("create: %v", err)
	}
	if err = shopOnly.SetActive(ctx, saved.ID, true); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("set active: %v", err)
	}
}

// countingDB counts the reads that reach the database, slowly enough
// that concurrent pages meet in one.
type countingDB struct {
	app.DB
	reads atomic.Int64
}

func (c *countingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.reads.Add(1)
	time.Sleep(50 * time.Millisecond)
	return c.DB.QueryRow(ctx, sql, args...)
}

func (c *countingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.reads.Add(1)
	return c.DB.Query(ctx, sql, args...)
}

// TestSlotsReadOnceWhenExpired: pages that find the minute's ads expired
// together wait for one read of the settings and one of the banners.
func TestSlotsReadOnceWhenExpired(t *testing.T) {
	db := &countingDB{DB: open(t, infra.AppURL)}
	svc := app.NewService(app.Deps{DB: db, IDs: ulid.NewGenerator(), Pick: func(int) int { return 0 }})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := svc.Slots(t.Context(), "AE"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := db.reads.Load(); n != 2 {
		t.Fatalf("%d reads for 20 pages, want 2", n)
	}
}
