package web

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Feeds says whether a key opens the store's product feeds
// (marketing.Feeds, ADR-0044).
type Feeds interface {
	FeedAllowed(ctx context.Context, key string) (bool, error)
}

// Product feeds: Google Merchant Center and Meta Commerce Manager fetch
// them every few hours, so the edge keeps each for an hour; a product
// change or a new key purges them.
const (
	feedEdge = "public, max-age=0, s-maxage=3600"
	// maxFeedPages bounds a feed: 500 pages of catalog.MaxStorefrontLimit
	// products (24,000).
	maxFeedPages = 500
	// Feed text limits (Google's; Meta's are as long or longer).
	maxFeedTitle       = 150
	maxFeedDescription = 5000
	maxFeedImages      = 11 // the image and ten more
)

// Feed requests that reach the origin are bounded per address (the keys
// one may try) and per store (the edge serves the right key's copy; its
// misses are rare).
var (
	feedPerIP    = ratelimit.Limit{Burst: 20, Period: time.Minute}  //nolint:gochecknoglobals // constant policy
	feedPerStore = ratelimit.Limit{Burst: 120, Period: time.Minute} //nolint:gochecknoglobals // constant policy
)

// feedRoute serves /feeds/<feed>.xml?key=…: the store's products, one
// item per variant, when key opens its feeds; a 404 otherwise.
func (s *Shop) feedRoute(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, rest string) bool {
	name, ok := strings.CutPrefix(rest, "/feeds/")
	if !ok {
		return false
	}
	name, ok = strings.CutSuffix(name, ".xml")
	feed := marketing.Feed(name)
	if !ok || s.feeds == nil || (feed != marketing.FeedGoogle && feed != marketing.FeedMeta) {
		feedNotFound(w)
		return true
	}
	if !isRead(r) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return true
	}
	key, ok := onlyKey(r.URL.RawQuery)
	if !ok {
		feedNotFound(w)
		return true
	}
	if allow, retry := s.allowGuarded(r, store, "feed", feedPerIP, feedPerStore); !allow {
		w.Header().Set("Cache-Control", "no-store")
		if retry == 0 { // the limiter is down: never serve the catalog unguarded
			w.Header().Set("Retry-After", "5")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return true
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return true
	}
	allowed, err := s.feeds.FeedAllowed(r.Context(), key)
	switch {
	case err != nil:
		s.unavailable(w, r, err)
	case !allowed:
		feedNotFound(w)
	default:
		s.serveFeed(w, r, store, p, feed)
	}
	return true
}

// onlyKey reads a feed URL's query: "key=<key>" and nothing else, so no
// other parameter can make a request the edge has no copy of.
func onlyKey(raw string) (string, bool) {
	key, ok := strings.CutPrefix(raw, "key=")
	if !ok || key == "" || strings.ContainsAny(key, "&;=%+") {
		return "", false
	}
	return key, true
}

// feedNotFound answers a feed that is off, unknown or asked with a wrong
// key alike, and keeps no copy anywhere.
func feedNotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
}

// serveFeed streams the feed a page of products at a time, so a large
// catalog never sits in memory. The first page is read before anything
// is sent (a database failure is a 503); a later failure aborts the
// response, so neither the edge nor the platform keeps a cut feed.
func (s *Shop) serveFeed(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, feed marketing.Feed) {
	ctx := r.Context()
	q := catalog.StorefrontQuery{Limit: catalog.MaxStorefrontLimit, Feed: true}
	page, err := s.catalog.ListProducts(ctx, q)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/xml; charset=utf-8")
	h.Set("Cache-Control", feedEdge)
	h.Set("Cache-Tag", strings.Join([]string{"s-" + store.String(), "l-" + store.String(), "f-" + store.String()}, ","))
	h.Set("X-Robots-Tag", "noindex")
	h.Add("Vary", "Accept-Encoding")
	var out io.Writer = w
	var gz *gzip.Writer
	if acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		gz = gzip.NewWriter(w)
		out = gz
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	fw := &feedWriter{w: bufio.NewWriterSize(out, 32<<10), feed: feed}
	fw.open(p.v.name, p.abs(p.l, "/"))
	for n := 1; ; n++ {
		for _, pr := range page.Items {
			fw.product(p, pr)
		}
		if page.NextCursor == "" || n == maxFeedPages {
			break
		}
		q.Cursor = page.NextCursor
		if page, err = s.catalog.ListProducts(ctx, q); err != nil {
			s.log.ErrorContext(ctx, "storefront: feed", "feed", feed, "err", err)
			panic(http.ErrAbortHandler) //nolint:forbidigo // net/http's contract: the only way to cut a response short, so the client sees it fail
		}
	}
	fw.close()
	if err = fw.err; err == nil {
		err = fw.w.Flush()
	}
	if err == nil && gz != nil {
		err = gz.Close()
	}
	if err != nil {
		s.log.DebugContext(ctx, "storefront: feed client went away", "err", err)
	}
}

// feedWriter writes an RSS 2.0 feed with Google's g: elements, which
// Meta reads too. It keeps the first write error and writes nothing
// after it.
type feedWriter struct {
	w    *bufio.Writer
	feed marketing.Feed
	err  error
}

func (f *feedWriter) put(s string) {
	if f.err == nil {
		_, f.err = f.w.WriteString(s)
	}
}

func (f *feedWriter) open(store, link string) {
	f.put(xml.Header + `<rss version="2.0" xmlns:g="http://base.google.com/ns/1.0">` + "\n<channel>\n")
	f.el("title", store)
	f.el("link", link)
	f.el("description", store)
}

func (f *feedWriter) close() { f.put("</channel>\n</rss>\n") }

// el writes <name>text</name>, escaped; nothing for empty text.
func (f *feedWriter) el(name, text string) {
	if text == "" {
		return
	}
	f.put("<" + name + ">")
	if f.err == nil {
		f.err = xml.EscapeText(f.w, []byte(text))
	}
	f.put("</" + name + ">\n")
}

// product writes an item per variant. Samples (never sellable) and
// products without a picture (both platforms refuse an item without one)
// are left out.
func (f *feedWriter) product(p *pageCtx, pr catalog.StorefrontProduct) {
	var images []string
	for _, img := range pr.Images {
		if img.FeedURL != "" && len(images) < maxFeedImages {
			images = append(images, img.FeedURL)
		}
	}
	if pr.Sample || len(images) == 0 {
		return
	}
	link := p.abs(p.l, productPath(pr.Slug))
	desc := strings.TrimSpace(pr.Description)
	if desc == "" {
		desc = pr.Title // both require one
	}
	desc = truncate(desc, maxFeedDescription-1)
	size, color := -1, -1
	for i, o := range pr.Options {
		switch strings.ToLower(strings.TrimSpace(o.Name)) {
		case "size", "المقاس", "مقاس", "الحجم", "حجم":
			size = i
		case "color", "colour", "اللون", "لون":
			color = i
		}
	}
	for _, v := range pr.Variants {
		f.put("<item>\n")
		f.el("g:id", v.ID.String())
		title, vlink := pr.Title, link
		if len(pr.Variants) > 1 {
			f.el("g:item_group_id", pr.ID.String())
			title += " (" + strings.Join(v.OptionValues, " / ") + ")"
			vlink += "?variant=" + v.ID.String() // the page opens on this variant
		}
		f.el("g:title", truncate(title, maxFeedTitle-1))
		f.el("g:description", desc)
		f.el("g:link", vlink)
		f.el("g:image_link", images[0])
		for _, img := range images[1:] {
			f.el("g:additional_image_link", img)
		}
		f.el("g:availability", f.availability(v.Available))
		if v.CompareAt != nil && v.CompareAt.Amount() > v.Price.Amount() {
			f.el("g:price", feedPrice(*v.CompareAt))
			f.el("g:sale_price", feedPrice(v.Price))
		} else {
			f.el("g:price", feedPrice(v.Price))
		}
		f.el("g:condition", "new")
		f.el("g:brand", p.v.name)
		if f.feed == marketing.FeedGoogle {
			f.el("g:identifier_exists", "no") // no GTIN or MPN to give
		}
		if size >= 0 && size < len(v.OptionValues) {
			f.el("g:size", v.OptionValues[size])
		}
		if color >= 0 && color < len(v.OptionValues) {
			f.el("g:color", v.OptionValues[color])
		}
		f.put("</item>\n")
	}
}

// availability is each platform's word for it.
func (f *feedWriter) availability(ok bool) string {
	switch {
	case f.feed == marketing.FeedMeta && ok:
		return "in stock"
	case f.feed == marketing.FeedMeta:
		return "out of stock"
	case ok:
		return "in_stock"
	}
	return "out_of_stock"
}

// feedPrice is "450.00 EGP".
func feedPrice(m money.Money) string { return m.Decimal() + " " + string(m.Currency()) }
