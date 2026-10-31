package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Ads in free stores (ADR-0062, approved 2026-10-31): the platform's
// banners first, then Google AdSense, in four placements, at most two a
// page, never in the cart, checkout, order or thank-you pages. A store
// with the ad_free feature shows none: paying moves it to a new view
// (ADR-0050), so the ads go at once.

// Ads is the ads module's side for shops (ads.Shop).
type Ads interface {
	Slots(ctx context.Context, country string) (map[ads.Placement]ads.Slot, error)
	PublisherID(ctx context.Context) (string, error)
	AdsTxt(ctx context.Context) (string, error)
	Click(ctx context.Context, id ulid.ID, count bool) (string, bool, error)
}

// Plan tells free stores (entitlements).
type Plan interface {
	Allowed(ctx context.Context, f entitlements.Feature) (bool, error)
}

// adsView is a free store's ads, decided with its view.
type adsView struct {
	slots     map[ads.Placement]ads.Slot
	publisher string                // AdSense's, when a slot shows its unit
	images    map[ulid.ID][2]string // a banner's AVIF and WebP, signed
}

func (a *adsView) avif(b *ads.Banner) string { return a.images[b.ID][0] }
func (a *adsView) webp(b *ads.Banner) string { return a.images[b.ID][1] }

// adsOf reads a store's ads: nil for a store with ad_free, or when either
// side is not wired or cannot answer (no ads is the safe failure).
func (s *Shop) adsOf(ctx context.Context, country geo.Country) *adsView {
	if s.ads == nil || s.plan == nil {
		return nil
	}
	free, err := s.plan.Allowed(ctx, entitlements.AdFree)
	if err != nil {
		s.log.WarnContext(ctx, "storefront: ad_free", "err", err)
		return nil
	}
	if free {
		return nil
	}
	slots, err := s.ads.Slots(ctx, string(country))
	if err != nil {
		s.log.WarnContext(ctx, "storefront: ads", "err", err)
		return &adsView{} // a free store still says so in its footer
	}
	v := &adsView{slots: slots, images: map[ulid.ID][2]string{}}
	for _, sl := range slots {
		if b := sl.Banner; b != nil {
			avif, webp := s.bannerImage(b.ImageKey)
			v.images[b.ID] = [2]string{avif, webp}
		}
	}
	if hasUnit(slots) {
		if v.publisher, err = s.ads.PublisherID(ctx); err != nil || v.publisher == "" {
			s.log.WarnContext(ctx, "storefront: ad publisher", "err", err)
			for p, sl := range slots {
				sl.Unit = ""
				slots[p] = sl
			}
		}
	}
	return v
}

func hasUnit(slots map[ads.Placement]ads.Slot) bool {
	for _, sl := range slots {
		if sl.Unit != "" {
			return true
		}
	}
	return false
}

// free reports whether the store is on the free plan (its footer says ads
// show, and why).
func (v *storeView) free() bool { return v.ads != nil }

// gridAdAfter is how many of a category's cards come before its grid ad.
const gridAdAfter = 4

// showAds says which placements the page has (at most two); a preview,
// or a store with ad_free, shows none.
func (p *pageCtx) showAds(places ...ads.Placement) {
	if p.v.ads == nil || p.preview != "" {
		return
	}
	p.ads = places[:min(len(places), 2)]
}

// slot is what placement shows on this page; ok is false when nothing.
func (p *pageCtx) slot(place ads.Placement) (ads.Slot, bool) {
	if p.v.ads == nil || !slices.Contains(p.ads, place) {
		return ads.Slot{}, false
	}
	sl, ok := p.v.ads.slots[place]
	return sl, ok && !sl.Empty()
}

// adSense reports whether the page shows an AdSense unit: it then loads
// ads.js and widens its CSP.
func (p *pageCtx) adSense() bool {
	for _, place := range p.ads {
		if sl, ok := p.slot(place); ok && sl.Unit != "" {
			return true
		}
	}
	return false
}

// AdSense's origins: its loader, its frames and its reporting. Only pages
// that show a unit carry them.
func adSenseSources() csp.Sources {
	return csp.Sources{
		Script:  []string{"https://pagead2.googlesyndication.com", "https://*.adtrafficquality.google"},
		Connect: []string{"https://pagead2.googlesyndication.com", "https://*.adtrafficquality.google"},
		Frame:   []string{"https://googleads.g.doubleclick.net", "https://tpc.googlesyndication.com", "https://*.googlesyndication.com", "https://www.google.com"},
	}
}

// sources are the page's third-party origins: the store's pixels, plus
// AdSense's when the page shows a unit.
func (p *pageCtx) sources() csp.Sources {
	src := p.v.pxSources()
	if p.adSense() {
		a := adSenseSources()
		src.Script = append(slices.Clone(src.Script), a.Script...)
		src.Connect = append(slices.Clone(src.Connect), a.Connect...)
		src.Frame = append(slices.Clone(src.Frame), a.Frame...)
	}
	return src
}

// bannerImage is a banner's picture, signed like products' images.
func (s *Shop) bannerImage(key string) (avif, webp string) {
	return s.signer.URL(key, 800, "avif"), s.signer.URL(key, 800, "webp")
}

// Click limits (CLAUDE.md: public endpoints per IP and per store): a
// shopper's clicks on one banner count once a minute, and an address or
// a store past its limit still reaches the link, uncounted.
var (
	clickPerBanner = ratelimit.Limit{Burst: 1, Period: time.Minute}   //nolint:gochecknoglobals // constant policy
	clickPerIP     = ratelimit.Limit{Burst: 20, Period: time.Minute}  //nolint:gochecknoglobals // constant policy
	clickPerStore  = ratelimit.Limit{Burst: 600, Period: time.Minute} //nolint:gochecknoglobals // constant policy
)

// countClick says whether a click on banner counts; a limiter outage
// counts nothing (no click inflation), the shopper still goes on.
func (s *Shop) countClick(r *http.Request, p *pageCtx, banner ulid.ID) bool {
	ip := s.clientIP(r)
	ok, _, err := s.limited(r.Context(), limitCheck{"adclick:ip:" + ip + ":" + banner.String(), clickPerBanner},
		limitCheck{"adclick:ip:" + ip, clickPerIP}, limitCheck{"adclick:store:" + p.store, clickPerStore})
	if err != nil {
		s.log.WarnContext(r.Context(), "storefront: ad click limiter unavailable", "err", err)
		return false
	}
	return ok
}

// adRoute serves a free store's /ads.txt and /ad/{banner}: the click is
// counted, then the shopper goes to the banner's link. Paid stores get
// 404 for both.
func (s *Shop) adRoute(w http.ResponseWriter, r *http.Request, _ tenant.StoreID, p *pageCtx, rest string) bool {
	id, isClick := strings.CutPrefix(rest, "/ad/")
	if rest != "/ads.txt" && !isClick {
		return false
	}
	if !isRead(r) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return true
	}
	if !p.v.free() || s.ads == nil {
		http.NotFound(w, r)
		return true
	}
	ctx := r.Context()
	if !isClick {
		body, err := s.ads.AdsTxt(ctx)
		if err != nil {
			s.unavailable(w, r, err)
			return true
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if _, err = w.Write([]byte(body)); err != nil {
			s.log.DebugContext(ctx, "storefront: client went away", "err", err)
		}
		return true
	}
	banner, err := ulid.Parse(id)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	link, ok, err := s.ads.Click(ctx, banner, s.countClick(r, p, banner))
	if err != nil {
		s.unavailable(w, r, err)
		return true
	}
	if u, perr := url.Parse(link); perr != nil || u.Scheme != "https" || u.Host == "" {
		ok = false // the console only saves https links; never send a shopper elsewhere
	}
	if !ok {
		http.Redirect(w, r, p.localize(p.l, "/"), http.StatusFound) // a banner taken down: the store's home
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "origin")
	http.Redirect(w, r, link, http.StatusFound) //nolint:gosec // G710: the console's https link of a banner on show, checked above
	return true
}
