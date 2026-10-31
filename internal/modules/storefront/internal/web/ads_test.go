package web_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

const publisher = "ca-pub-1234567890123456"

// fakeAds are the platform's ads; clicks are counted per banner.
type fakeAds struct {
	slots     map[ads.Placement]ads.Slot
	publisher string
	err       error
	mu        sync.Mutex
	clicks    map[ulid.ID]int
	countries []string
}

func (a *fakeAds) Slots(_ context.Context, country string) (map[ads.Placement]ads.Slot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.countries = append(a.countries, country)
	if a.err != nil {
		return nil, a.err
	}
	out := make(map[ads.Placement]ads.Slot, len(a.slots))
	for p, s := range a.slots {
		out[p] = s
	}
	return out, nil
}

func (a *fakeAds) PublisherID(context.Context) (string, error) { return a.publisher, nil }

func (a *fakeAds) AdsTxt(context.Context) (string, error) {
	if a.publisher == "" {
		return "", nil
	}
	return "google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0\n", nil
}

func (a *fakeAds) Click(_ context.Context, id ulid.ID) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if s.Banner != nil && s.Banner.ID == id {
			if a.clicks == nil {
				a.clicks = map[ulid.ID]int{}
			}
			a.clicks[id]++
			return s.Banner.Link, true, nil
		}
	}
	return "", false, nil
}

// fakePlan answers ad_free.
type fakePlan struct {
	adFree bool
	err    error
	asked  *atomic.Int64
}

func (p fakePlan) Allowed(_ context.Context, f entitlements.Feature) (bool, error) {
	if p.asked != nil {
		p.asked.Add(1)
	}
	if f != entitlements.AdFree {
		return false, errors.New("unexpected feature " + string(f))
	}
	return p.adFree, p.err
}

var houseBanner = &ads.Banner{ID: gen.New(), Title: "افتح متجرك مجانًا", ImageKey: "platform/ads/x.png", Width: 640, Height: 200,
	Link: "https://iftahonline.com/start", Placement: ads.HomeTop, Weight: 10}

// mixed: our banner on the home's top, AdSense between products and under
// a product.
func mixed() *fakeAds {
	return &fakeAds{publisher: publisher, slots: map[ads.Placement]ads.Slot{
		ads.HomeTop:       {Placement: ads.HomeTop, Banner: houseBanner},
		ads.Grid:          {Placement: ads.Grid, Unit: "2222222222"},
		ads.ProductBottom: {Placement: ads.ProductBottom, Unit: "4444444444"},
		ads.CategoryTop:   {Placement: ads.CategoryTop},
	}}
}

func withAds(a *fakeAds, p fakePlan) func(*web.Deps) {
	return func(d *web.Deps) { d.Ads, d.Plan = a, p }
}

const (
	adSenseScript = `src="/assets/ads.`
	adsNote       = `<p class="ads-note">هذا المتجر على الباقة المجانية`
)

// TestAdsOnFreeStores: the home shows our banner on top and AdSense
// between products; a product, AdSense under its description; ads.js and
// AdSense's CSP origins only where a unit shows (ADR-0062).
func TestAdsOnFreeStores(t *testing.T) {
	t.Parallel()
	a := mixed()
	f := newFixture(t, withAds(a, fakePlan{}))

	rec := f.get(t, "/")
	body := rec.Body.String()
	wantContains(t, body,
		`<aside class="ad ad-home_top" aria-label="إعلان"><span class="ad-tag">إعلان</span> <a href="/ad/`+houseBanner.ID.String()+`" rel="sponsored nofollow">`,
		`<source type="image/avif" srcset="https://img.test/platform/ads/x.png/avif/0">`,
		`alt="افتح متجرك مجانًا" width="640" height="200" loading="lazy"`,
		`<ins class="adsbygoogle" data-ad-client="`+publisher+`" data-ad-slot="2222222222"`,
		adSenseScript, `data-client="`+publisher+`"`, adsNote)
	if strings.Count(body, `<aside class="ad `) > 2 {
		t.Fatal("more than two ads on a page")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"https://pagead2.googlesyndication.com", "https://googleads.g.doubleclick.net"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %s: %s", want, csp)
		}
	}
	if a.countries[0] != "EG" {
		t.Fatalf("asked for %v", a.countries)
	}

	body = f.get(t, "/p/cotton-tee").Body.String()
	wantContains(t, body, `<aside class="ad ad-product_bottom"`, `data-ad-slot="4444444444"`, adSenseScript)
	if strings.Contains(body, `class="ad ad-home_top"`) {
		t.Fatal("home's ad on a product")
	}

	// An empty placement keeps no space, and a short list (under five
	// products) is not interrupted.
	body = f.get(t, "/c/tshirts").Body.String()
	if strings.Contains(body, `class="ad `) || strings.Contains(body, adSenseScript) {
		t.Fatal("ads in a short category without a top ad")
	}
	a.mu.Lock()
	a.slots[ads.CategoryTop] = ads.Slot{Placement: ads.CategoryTop, Unit: "3333333333"}
	a.mu.Unlock()
	f = newFixture(t, withAds(a, fakePlan{}))
	wantContains(t, f.get(t, "/c/tshirts").Body.String(), `<aside class="ad ad-category_top"`, `data-ad-slot="3333333333"`, adSenseScript)
}

// TestAdsOnlyBannersNoAdSense: a page with our banners only loads no
// ads.js and opens no CSP origin.
func TestAdsOnlyBannersNoAdSense(t *testing.T) {
	t.Parallel()
	a := &fakeAds{slots: map[ads.Placement]ads.Slot{ads.HomeTop: {Placement: ads.HomeTop, Banner: houseBanner},
		ads.Grid: {Placement: ads.Grid, Unit: "2222222222"}}} // a unit, but no publisher: dropped
	f := newFixture(t, withAds(a, fakePlan{}))
	rec := f.get(t, "/")
	body := rec.Body.String()
	wantContains(t, body, `ad-home_top`, adsNote)
	if strings.Contains(body, "adsbygoogle") || strings.Contains(body, adSenseScript) {
		t.Fatal("AdSense without a publisher")
	}
	if c := rec.Header().Get("Content-Security-Policy"); strings.Contains(c, "googlesyndication") {
		t.Fatalf("CSP %s", c)
	}
}

// TestNoAds: a store with ad_free, without the modules, or whose plan
// cannot be read shows no ad and no note; a failing ads module still
// leaves the note; the cart never shows ads.
func TestNoAds(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		opt  func(*web.Deps)
		note bool
	}{
		"ad_free":      {withAds(mixed(), fakePlan{adFree: true}), false},
		"no module":    {func(*web.Deps) {}, false},
		"plan fails":   {withAds(mixed(), fakePlan{err: errors.New("down")}), false},
		"ads fail":     {withAds(&fakeAds{err: errors.New("down")}, fakePlan{}), true},
		"nothing live": {withAds(&fakeAds{}, fakePlan{}), true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tc.opt)
			for _, path := range []string{"/", "/p/cotton-tee", "/c/tshirts"} {
				rec := f.get(t, path)
				body := rec.Body.String()
				if rec.Code != http.StatusOK || strings.Contains(body, `class="ad `) || strings.Contains(body, adSenseScript) {
					t.Fatalf("%s: %d, shows ads", path, rec.Code)
				}
				if strings.Contains(body, adsNote) != tc.note {
					t.Fatalf("%s: note shown = %v", path, !tc.note)
				}
				if strings.Contains(rec.Header().Get("Content-Security-Policy"), "googlesyndication") {
					t.Fatalf("%s: AdSense's CSP", path)
				}
			}
		})
	}
	f := newFixture(t, withAds(mixed(), fakePlan{}))
	if body := f.get(t, "/cart").Body.String(); strings.Contains(body, `class="ad `) || strings.Contains(body, adSenseScript) {
		t.Fatal("ads in the cart")
	}
	f.content.previews = map[string]domain.Draft{"live-token": {Home: &domain.HomeDraft{Title: "مسودة", Sections: domain.DefaultHome().Sections}}}
	if body := f.get(t, "/?preview=live-token").Body.String(); !strings.Contains(body, "preview-bar") || strings.Contains(body, `class="ad `) {
		t.Fatal("ads in a preview")
	}
}

// TestAdRoutes: a free store serves /ads.txt and counts a banner's click
// on its way to the link; a paid store has neither.
func TestAdRoutes(t *testing.T) {
	t.Parallel()
	a := mixed()
	f := newFixture(t, withAds(a, fakePlan{}))

	rec := f.get(t, "/ads.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != "google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0\n" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("ads.txt = %d %q", rec.Code, rec.Body.String())
	}
	rec = f.get(t, "/ad/"+houseBanner.ID.String())
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != houseBanner.Link || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Referrer-Policy") != "origin" {
		t.Fatalf("click = %d %v", rec.Code, rec.Header())
	}
	if a.clicks[houseBanner.ID] != 1 {
		t.Fatalf("clicks = %v", a.clicks)
	}
	if rec = f.get(t, "/ad/"+gen.New().String()); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("a banner taken down = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec = f.get(t, "/ad/junk"); rec.Code != http.StatusNotFound {
		t.Fatalf("junk = %d", rec.Code)
	}
	if rec = f.post(t, "/ad/"+houseBanner.ID.String(), nil, nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post = %d", rec.Code)
	}

	paid := newFixture(t, withAds(mixed(), fakePlan{adFree: true}))
	for _, path := range []string{"/ads.txt", "/ad/" + houseBanner.ID.String()} {
		if rec = paid.get(t, path); rec.Code != http.StatusNotFound {
			t.Fatalf("paid %s = %d", path, rec.Code)
		}
	}
}
