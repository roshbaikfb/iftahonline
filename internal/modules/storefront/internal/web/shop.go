package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the shop's dependencies.
type Deps struct {
	Catalog Catalog
	Stores  Stores
	Content Content
	Signer  Signer
	Assets  *Assets
	Log     *slog.Logger
	Cache   CacheConfig
	// HTTPS builds absolute URLs (canonical, hreflang, structured data)
	// with https; off for local development.
	HTTPS bool
	// EdgeTTL is how long the edge may keep a page (s-maxage); 60s or
	// less unless the worker purges (ADR-0019). Zero means 60s.
	EdgeTTL time.Duration
	// Carts serves the cart; Orders the thank-you page (nil until the
	// orders module exists: the page is then a 404).
	Carts  Carts
	Orders Orders
	// Confirmations backs the SMS link's page (/o/<token>); nil makes
	// it a 404.
	Confirmations Confirmations
	// Payments offers card payment at checkout; nil means cash on
	// delivery only.
	Payments Payments
	// Limiter rate-limits cart writes and orders; nil disables.
	Limiter Limiter
	// Captcha verifies the challenge risky order submissions must pass,
	// shown with CaptchaSiteKey (ADR-0022). Without them, such a
	// submission is refused as busy.
	Captcha        CaptchaVerifier
	CaptchaSiteKey string
	// ClientIP is the shopper's address as the trusted proxies report it.
	ClientIP func(*http.Request) string
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
	// Search is true when Catalog.Search works: pages show the search box.
	Search bool
	// Pixels are the store's ad pixels (ADR-0043); nil loads none.
	Pixels Pixels
	// Conversions reports the cart's and checkout's conversions from the
	// server too; nil reports none.
	Conversions Conversions
	// Feeds opens the store's product feeds (ADR-0044); nil serves none.
	Feeds Feeds
	// Added keeps an AddToCart for the cart page (ADR-0043); nil reports
	// no AddToCart.
	Added Added
}

// Shop serves a store's pages. The tenant must already be in the request
// context (the host was resolved before).
type Shop struct {
	catalog     Catalog
	stores      Stores
	content     Content
	signer      Signer
	assets      *Assets
	log         *slog.Logger
	caches      *caches
	scheme      string
	edge        string // Cache-Control of a page
	carts       Carts
	orders      Orders
	confirms    Confirmations
	payments    Payments
	limiter     Limiter
	captcha     CaptchaVerifier
	siteKey     string
	clientIP    func(*http.Request) string
	now         func() time.Time
	searchOn    bool // Catalog.Search works: pages show the search box
	pixels      Pixels
	conversions Conversions
	feeds       Feeds
	added       Added
	ids         *ulid.Generator // event IDs of conversions (ADR-0043)
}

// New builds the shop.
func New(d Deps) (*Shop, error) {
	if d.Catalog == nil || d.Stores == nil || d.Content == nil || d.Carts == nil || d.Signer == nil || d.Assets == nil || d.Log == nil {
		return nil, errors.New("web: missing dependency")
	}
	if d.Cache == (CacheConfig{}) {
		d.Cache = DefaultCacheConfig()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ClientIP == nil {
		d.ClientIP = func(r *http.Request) string { return r.RemoteAddr }
	}
	scheme := "http"
	if d.HTTPS {
		scheme = "https"
	}
	if d.EdgeTTL <= 0 {
		d.EdgeTTL = time.Minute
	}
	edge := "public, max-age=0, s-maxage=" + strconv.Itoa(int(d.EdgeTTL/time.Second))
	return &Shop{catalog: d.Catalog, stores: d.Stores, content: d.Content, signer: d.Signer, assets: d.Assets,
		log: d.Log, caches: newCaches(d.Cache), scheme: scheme, edge: edge, carts: d.Carts, orders: d.Orders, confirms: d.Confirmations,
		payments: d.Payments, limiter: d.Limiter, captcha: d.Captcha, siteKey: d.CaptchaSiteKey, clientIP: d.ClientIP, now: d.Now, searchOn: d.Search,
		pixels: d.Pixels, conversions: d.Conversions, feeds: d.Feeds, added: d.Added, ids: ulid.NewGenerator()}, nil
}

// Invalidate drops everything cached for a store: its next request
// renders fresh (ADR-0018).
func (s *Shop) Invalidate(store tenant.StoreID) { s.caches.Invalidate(store) }

// result is a rendered response or a redirect.
type result struct {
	cached
	redirect string
}

// ServeHTTP serves every storefront page (GET and HEAD) and the cart's
// form posts.
func (s *Shop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	store, err := tenant.FromContext(ctx)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	v, prefix, err := s.view(ctx, store)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	l, rest := v.locale, r.URL.Path
	if alt := "/" + string(v.locale.Other()); rest == alt || strings.HasPrefix(rest, alt+"/") {
		l, rest = v.locale.Other(), strings.TrimPrefix(rest, alt)
		if rest == "" {
			rest = "/"
		}
	}
	p := &pageCtx{v: v, store: store.String(), l: l, base: s.scheme + "://" + r.Host, path: rest, jsURL: s.assets.jsURL, pxURL: s.assets.pxURL, fonts: s.assets.fontPreload(v.theme, l),
		search: s.searchOn, track: s.orders != nil}
	if s.cartRoute(w, r, store, p, rest) || s.orderRoute(w, r, store, p, rest) || s.feedRoute(w, r, store, p, rest) {
		return
	}
	if token := r.URL.Query().Get("preview"); token != "" && isRead(r) {
		if s.servePreview(w, r, p, token, rest) {
			return
		}
	}
	if !isRead(r) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	q := parseQuery(rest, r.URL.Query())
	key := q.cacheKey(l, rest)
	if c, ok := s.caches.page(prefix, key); ok {
		s.write(w, r, c)
		return
	}
	if rest == "/search" && q.key != "" {
		if ok, retry := s.allowSearch(r, store); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
	}
	res, err := s.route(ctx, p, rest, q)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	if res.redirect != "" {
		http.Redirect(w, r, res.redirect, http.StatusMovedPermanently)
		return
	}
	res.tags = p.cacheTags()
	s.caches.putPage(prefix, key, res.cached)
	s.write(w, r, res.cached)
}

func (s *Shop) view(ctx context.Context, store tenant.StoreID) (*storeView, string, error) {
	v, prefix, ok := s.caches.view(store)
	if ok {
		return v, prefix, nil
	}
	v, err := s.buildView(ctx)
	if err != nil {
		return nil, "", err
	}
	s.caches.views.Add(prefix, v)
	return v, prefix, nil
}

// route renders the page at rest (the path without its language prefix).
func (s *Shop) route(ctx context.Context, p *pageCtx, rest string, q query) (result, error) {
	seg := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case rest == "/":
		return s.home(ctx, p)
	case rest == "/search":
		return s.search(ctx, p, q)
	case len(seg) == 2 && seg[0] == "p":
		return s.slugged(p, seg[1], productPath, func(slug string) (result, error) { return s.product(ctx, p, slug) })
	case len(seg) == 2 && seg[0] == "c":
		return s.slugged(p, seg[1], categoryPath, func(slug string) (result, error) { return s.category(ctx, p, slug, q.after) })
	case len(seg) == 2 && seg[0] == "pages":
		return s.slugged(p, seg[1], func(slug string) string { return "/pages/" + slug }, func(slug string) (result, error) { return s.landing(ctx, p, slug) })
	case len(seg) == 2 && seg[0] == "policies":
		return s.policy(ctx, p, seg[1])
	}
	return s.notFound(p)
}

// slugged sends an upper-case slug to its lower-case page (slugs are
// lower-case; shoppers and old links are not always).
func (s *Shop) slugged(p *pageCtx, slug string, path func(string) string, page func(string) (result, error)) (result, error) {
	if lower := strings.ToLower(slug); lower != slug {
		return result{redirect: p.localize(p.l, path(lower))}, nil
	}
	return page(slug)
}

func (s *Shop) home(ctx context.Context, p *pageCtx) (result, error) {
	page, err := s.content.GetHome(ctx)
	if err != nil {
		return result{}, err
	}
	if d := p.homeDraft; d != nil {
		page.Title, page.Description, page.Sections = d.Title, d.Description, d.Sections
	}
	secs, err := s.loadSections(ctx, p, page.Sections, s.now())
	if err != nil {
		return result{}, err
	}
	p.title = p.v.name
	if page.Title != "" {
		p.title = page.Title
	}
	p.desc = page.Description
	if !page.ID.IsZero() {
		p.tag("pg-" + page.ID.String())
	}
	p.tagListing()
	return s.render(p, http.StatusOK, sectionsPage(p, secs))
}

func (s *Shop) product(ctx context.Context, p *pageCtx, slug string) (result, error) {
	pr, err := s.catalog.ProductBySlug(ctx, slug)
	if errors.Is(err, catalog.ErrProductNotFound) {
		return s.notFound(p)
	}
	if err != nil {
		return result{}, err
	}
	var crumb *catalog.Category
	for _, id := range pr.CategoryIDs {
		if c, ok := p.v.categoryByID(id); ok {
			crumb = &c
			break
		}
	}
	// "You may also like": the first category's newest, in one query.
	var related []catalog.StorefrontProduct
	if crumb != nil {
		page, listErr := s.catalog.ListProducts(ctx, catalog.StorefrontQuery{CategoryID: &crumb.ID, Limit: maxRelated + 1})
		if listErr != nil {
			return result{}, listErr
		}
		related = page.Items
		p.tagProducts(related) // a related product's edit renews the page; a new one shows within the cache's TTL
	}
	p.head, p.back = headProduct, "/"
	if crumb != nil {
		p.back = categoryPath(crumb.Slug)
	}
	p.title, p.tags = pr.Title+" | "+p.v.name, append(p.tags, "p-"+pr.ID.String())
	if d := paragraphs(pr.Description); len(d) > 0 {
		p.desc = truncate(d[0], 300)
	}
	if img, ok := firstImage(pr); ok {
		p.image = img.src
	}
	p.jsonLD = productLD(p, pr, crumb)
	if from := pr.PriceFrom(); len(pr.Variants) > 0 {
		p.event(EventViewContent, "", from.Price, []string{from.ID.String()})
	}
	return s.render(p, http.StatusOK, productPage(p, crumb, s.productView(p, pr, related)))
}

func (s *Shop) category(ctx context.Context, p *pageCtx, slug, after string) (result, error) {
	c, ok := p.v.category(slug)
	if !ok {
		return s.notFound(p)
	}
	page, err := s.catalog.ListProducts(ctx, catalog.StorefrontQuery{CategoryID: &c.ID, Cursor: after, Limit: 24})
	if err != nil {
		return result{}, err
	}
	p.title = c.Name + " | " + p.v.name
	p.tag("c-" + c.ID.String())
	p.tagListing()
	p.tagProducts(page.Items)
	more := ""
	if page.NextCursor != "" {
		more = p.localize(p.l, categoryPath(c.Slug)) + "?after=" + page.NextCursor
	}
	return s.render(p, http.StatusOK, categoryPage(p, c, page.Items, more))
}

func (s *Shop) landing(ctx context.Context, p *pageCtx, slug string) (result, error) {
	page, err := s.content.PublishedLanding(ctx, slug)
	if errors.Is(err, app.ErrPageNotFound) {
		return s.notFound(p)
	}
	if err != nil {
		return result{}, err
	}
	secs, err := s.loadSections(ctx, p, page.Sections, s.now())
	if err != nil {
		return result{}, err
	}
	p.title, p.desc = page.Title+" | "+p.v.name, page.Description
	p.tag("pg-" + page.ID.String())
	p.tagListing()
	return s.render(p, http.StatusOK, sectionsPage(p, secs))
}

func (s *Shop) policy(ctx context.Context, p *pageCtx, slug string) (result, error) {
	page, err := s.content.PublishedPolicy(ctx, slug)
	if errors.Is(err, app.ErrPageNotFound) {
		return s.notFound(p)
	}
	if err != nil {
		return result{}, err
	}
	title := page.Title
	if title == "" {
		title = p.l.PolicyTitle(slug)
	}
	p.title = title + " | " + p.v.name
	p.tag("pg-" + page.ID.String())
	return s.render(p, http.StatusOK, policyPage(title, paragraphs(page.Body)))
}

func (s *Shop) notFound(p *pageCtx) (result, error) {
	p.title = p.l.T(MsgNotFoundTitle) + " | " + p.v.name
	return s.render(p, http.StatusNotFound, notFoundPage(p))
}

func (s *Shop) render(p *pageCtx, status int, body templ.Component) (result, error) {
	var buf bytes.Buffer
	buf.Grow(32 << 10)
	if err := layout(p, body).Render(context.Background(), &buf); err != nil {
		return result{}, fmt.Errorf("web: render: %w", err)
	}
	html := buf.Bytes()
	c := cached{status: status, body: html, gz: compress(html), styleHash: p.v.styleHash, px: p.v.pxSources(), language: p.l, private: p.preview != ""}
	if p.css != "" {
		c.pageStyle = styleHash(p.css)
	}
	return result{cached: c}, nil
}

// write sends a rendered page. Pages are the same for every shopper, so
// the edge may keep them for EdgeTTL; the worker purges their tags when
// what they show changes (ADR-0019).
func (s *Shop) write(w http.ResponseWriter, r *http.Request, c cached) {
	s.writeHeaders(w, c)
	h := w.Header()
	if c.private {
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Robots-Tag", "noindex")
		s.writeBody(w, r, c)
		return
	}
	if c.status == http.StatusOK {
		h.Set("Cache-Control", s.edge)
		h.Set("Cache-Tag", c.tags)
	} else {
		h.Set("Cache-Control", "public, max-age=0, s-maxage=30")
	}
	s.writeBody(w, r, c)
}

// writeBody sends the page, gzipped when the client accepts it (the edge
// usually does; the origin's own compression saves its egress and serves
// clients that reach it directly). Vary keeps the two versions apart in
// every cache.
func (s *Shop) writeBody(w http.ResponseWriter, r *http.Request, c cached) {
	body := c.body
	w.Header().Add("Vary", "Accept-Encoding")
	if c.gz != nil && acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		body = c.gz
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(c.status)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		s.log.DebugContext(r.Context(), "storefront: client went away", "err", err)
	}
}

// writeHeaders sets what every HTML page carries: its type, language and
// the CSP that allows exactly its inline style.
func (s *Shop) writeHeaders(w http.ResponseWriter, c cached) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	hashes := []string{c.styleHash}
	if c.pageStyle != "" {
		hashes = append(hashes, c.pageStyle)
	}
	if c.challenge {
		h.Set("Content-Security-Policy", csp.StorefrontChallenge(hashes...))
	} else {
		h.Set("Content-Security-Policy", csp.StorefrontWith(c.px, hashes...))
	}
	h.Set("Content-Language", string(c.language))
}

// unavailable answers when the store's data cannot be read (database
// down): a short, uncached 503 that asks the browser to retry.
func (s *Shop) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "storefront: render", "err", err)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "5")
	http.Error(w, "تعذر تحميل المتجر الآن. حاول مرة أخرى بعد قليل. · The store could not be loaded; please try again shortly.", http.StatusServiceUnavailable)
}

// truncate cuts s to at most n runes on a word boundary.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
