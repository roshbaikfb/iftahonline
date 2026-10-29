package web_test

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

const feedKey = "abcdefghijklmnopqrstuvwxyz234567"

// fakeFeeds opens the feeds with key.
type fakeFeeds struct {
	key string
	err error
}

func (f fakeFeeds) FeedAllowed(_ context.Context, key string) (bool, error) {
	return f.err == nil && key == f.key, f.err
}

func withFeeds(f fakeFeeds) func(*web.Deps) { return func(d *web.Deps) { d.Feeds = f } }

// feedItem is an item as Google and Meta read it.
type feedItem struct {
	ID          string   `xml:"http://base.google.com/ns/1.0 id"`
	Group       string   `xml:"http://base.google.com/ns/1.0 item_group_id"`
	Title       string   `xml:"http://base.google.com/ns/1.0 title"`
	Description string   `xml:"http://base.google.com/ns/1.0 description"`
	Link        string   `xml:"http://base.google.com/ns/1.0 link"`
	Image       string   `xml:"http://base.google.com/ns/1.0 image_link"`
	More        []string `xml:"http://base.google.com/ns/1.0 additional_image_link"`
	Available   string   `xml:"http://base.google.com/ns/1.0 availability"`
	Price       string   `xml:"http://base.google.com/ns/1.0 price"`
	Sale        string   `xml:"http://base.google.com/ns/1.0 sale_price"`
	Condition   string   `xml:"http://base.google.com/ns/1.0 condition"`
	Brand       string   `xml:"http://base.google.com/ns/1.0 brand"`
	Identifiers string   `xml:"http://base.google.com/ns/1.0 identifier_exists"`
	Size        string   `xml:"http://base.google.com/ns/1.0 size"`
	Color       string   `xml:"http://base.google.com/ns/1.0 color"`
}

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title string     `xml:"title"`
		Link  string     `xml:"link"`
		Items []feedItem `xml:"item"`
	} `xml:"channel"`
}

func parseFeed(t *testing.T, body []byte) rss {
	t.Helper()
	var f rss
	if err := xml.Unmarshal(body, &f); err != nil {
		t.Fatalf("feed is not XML: %v\n%s", err, body)
	}
	if f.Version != "2.0" {
		t.Fatalf("rss version %q", f.Version)
	}
	return f
}

func items(f rss) map[string]feedItem {
	out := map[string]feedItem{}
	for _, it := range f.Channel.Items {
		out[it.ID] = it
	}
	return out
}

// TestFeedGoogle: an item per variant, grouped by product, linked to the
// page open on it, with its JPEG, availability, price before a sale and
// size; the edge keeps it an hour under the store's tags.
func TestFeedGoogle(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	rec := f.get(t, "/feeds/google.xml?key="+feedKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	store := f.store.String()
	if h.Get("Content-Type") != "application/xml; charset=utf-8" || h.Get("Cache-Control") != "public, max-age=0, s-maxage=3600" ||
		h.Get("Cache-Tag") != "s-"+store+",l-"+store+",f-"+store || h.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("headers %v", h)
	}
	feed := parseFeed(t, rec.Body.Bytes())
	if feed.Channel.Title != "متجر منى" || feed.Channel.Link != "https://mona.iftahonline.com/" || len(feed.Channel.Items) != 3 {
		t.Fatalf("channel %+v", feed.Channel)
	}
	byID := items(feed)
	m, l := byID[shirtM.String()], byID[f.shirt.Variants[1].ID.String()]
	wantM := feedItem{ID: shirtM.String(), Group: f.shirt.ID.String(), Title: "تيشيرت قطن (M)", Description: "قطن مصري ١٠٠٪.\n\nيغسل في الغسالة.",
		Link: "https://mona.iftahonline.com/p/cotton-tee?variant=" + shirtM.String(), Image: "https://img.test/cotton-tee-0.jpg",
		Available: "in_stock", Price: "200.00 EGP", Sale: "150.00 EGP", Condition: "new", Brand: "متجر منى", Identifiers: "no", Size: "M"}
	if !equalItems(m, wantM) {
		t.Errorf("variant M\n got %+v\nwant %+v", m, wantM)
	}
	if l.Available != "out_of_stock" || l.Price != "160.00 EGP" || l.Sale != "" || l.Size != "L" || l.Group != f.shirt.ID.String() {
		t.Errorf("variant L %+v", l)
	}
	var cp feedItem
	for id, it := range byID {
		if id != shirtM.String() && id != f.shirt.Variants[1].ID.String() {
			cp = it
		}
	}
	if cp.Title != "كاب" || cp.Group != "" || cp.Link != "https://mona.iftahonline.com/p/cap" || cp.Price != "99.00 EGP" || cp.Size != "" {
		t.Errorf("one-variant product %+v", cp)
	}
	if n := f.catalog.calls0("list"); n != 1 {
		t.Errorf("%d list calls for one page", n)
	}
}

func equalItems(a, b feedItem) bool {
	return a.ID == b.ID && a.Group == b.Group && a.Title == b.Title && a.Description == b.Description && a.Link == b.Link &&
		a.Image == b.Image && len(a.More) == len(b.More) && a.Available == b.Available && a.Price == b.Price && a.Sale == b.Sale &&
		a.Condition == b.Condition && a.Brand == b.Brand && a.Identifiers == b.Identifiers && a.Size == b.Size && a.Color == b.Color
}

// TestFeedMeta: Meta's words for availability; no Google-only element.
func TestFeedMeta(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	rec := f.get(t, "/feeds/meta.xml?key="+feedKey)
	byID := items(parseFeed(t, rec.Body.Bytes()))
	if m := byID[shirtM.String()]; m.Available != "in stock" || m.Identifiers != "" || m.Sale != "150.00 EGP" {
		t.Fatalf("variant M %+v", m)
	}
	if l := byID[f.shirt.Variants[1].ID.String()]; l.Available != "out of stock" {
		t.Fatalf("variant L %+v", l)
	}
}

// TestFeedRefused: no feeds, an unknown feed, a wrong or missing key,
// and a write are refused, with nothing kept; a failure to check the key
// is a 503.
func TestFeedRefused(t *testing.T) {
	t.Parallel()
	on := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	off := newFixture(t)
	broken := newFixture(t, withFeeds(fakeFeeds{key: feedKey, err: errors.New("database down")}))
	for _, tc := range []struct {
		name string
		f    fixture
		path string
		want int
	}{
		{"wrong key", on, "/feeds/google.xml?key=" + strings.Repeat("a", 32), http.StatusNotFound},
		{"no key", on, "/feeds/google.xml", http.StatusNotFound},
		{"unknown feed", on, "/feeds/tiktok.xml?key=" + feedKey, http.StatusNotFound},
		{"not xml", on, "/feeds/google?key=" + feedKey, http.StatusNotFound},
		{"no feeds module", off, "/feeds/google.xml?key=" + feedKey, http.StatusNotFound},
		{"key unchecked", broken, "/feeds/google.xml?key=" + feedKey, http.StatusServiceUnavailable},
	} {
		rec := tc.f.get(t, tc.path)
		if rec.Code != tc.want || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "<item>") {
			t.Errorf("%s: %d %q", tc.name, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/feeds/google.xml?key="+feedKey, nil)
	rec := httptest.NewRecorder()
	on.h.ServeHTTP(rec, rq)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
	if n := on.catalog.calls0("list"); n != 0 {
		t.Errorf("refused feeds read the catalog %d times", n)
	}
}

// TestFeedRateLimit: keys tried from one address are limited.
func TestFeedRateLimit(t *testing.T) {
	t.Parallel()
	lim := &denyLimiter{}
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}), func(d *web.Deps) {
		d.Limiter = lim
		d.ClientIP = func(*http.Request) string { return "203.0.113.7" }
	})
	rec := f.get(t, "/feeds/google.xml?key="+feedKey)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || len(lim.keys) != 1 || lim.keys[0] != "feed:ip:203.0.113.7" {
		t.Fatalf("limited: %d %v", rec.Code, lim.keys)
	}
}

// TestFeedPages: a catalog of many pages streams whole, a page per query.
func TestFeedPages(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	const n = 2*catalog.MaxStorefrontLimit + 5
	ps := make([]catalog.StorefrontProduct, 0, n)
	for i := range n {
		ps = append(ps, product("منتج "+strconv.Itoa(i), "p-"+strconv.Itoa(i), 1000, true))
	}
	f.catalog.products = ps
	feed := parseFeed(t, f.get(t, "/feeds/google.xml?key="+feedKey).Body.Bytes())
	if len(feed.Channel.Items) != n || len(items(feed)) != n {
		t.Fatalf("%d items, want %d", len(feed.Channel.Items), n)
	}
	if calls := f.catalog.calls0("list"); calls != 3 {
		t.Fatalf("%d list calls, want 3", calls)
	}
}

// TestFeedLeavesOut: samples (never sellable) and products without a
// picture are not items; text is escaped; at most eleven pictures.
func TestFeedLeavesOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	sample := product("نموذج", "sample", 1000, false)
	sample.Sample = true
	bare := product("بلا صورة", "bare", 1000, true)
	bare.Images = nil
	odd := product(`Tee <b> & "co"`, "odd", 1000, true)
	odd.Description = "  "
	for range 14 {
		odd.Images = append(odd.Images, odd.Images[0])
	}
	f.catalog.products = []catalog.StorefrontProduct{sample, bare, odd}
	rec := f.get(t, "/feeds/meta.xml?key="+feedKey)
	feed := parseFeed(t, rec.Body.Bytes())
	if len(feed.Channel.Items) != 1 {
		t.Fatalf("items %+v", feed.Channel.Items)
	}
	it := feed.Channel.Items[0]
	if it.Title != `Tee <b> & "co"` || it.Description != it.Title || len(it.More) != 10 || strings.Contains(rec.Body.String(), "<b>") {
		t.Fatalf("item %+v", it)
	}
}

// TestFeedGzip: a client that takes gzip gets it; HEAD gets no body.
func TestFeedGzip(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/google.xml?key="+feedKey, nil)
	rq.Host = "mona.iftahonline.com"
	rq.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("headers %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if len(parseFeed(t, body).Channel.Items) != 3 {
		t.Fatal("gzipped feed lacks items")
	}
	rq = httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/feeds/google.xml?key="+feedKey, nil)
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %d bytes", rec.Code, rec.Body.Len())
	}
}

// TestFeedFails: a failure before anything is sent is a 503; a failure
// after aborts the response (net/http's ErrAbortHandler), so no cache
// keeps a cut feed.
func TestFeedFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	ps := make([]catalog.StorefrontProduct, 0, catalog.MaxStorefrontLimit+1)
	for i := range catalog.MaxStorefrontLimit + 1 {
		ps = append(ps, product("منتج", "p-"+strconv.Itoa(i), 1000, true))
	}
	f.catalog.products = ps
	f.catalog.listErr, f.catalog.failList = errors.New("database down"), 1
	defer func() {
		if r := recover(); r != http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does.
			t.Fatalf("recovered %v, want http.ErrAbortHandler", r)
		}
	}()
	f.get(t, "/feeds/google.xml?key="+feedKey)
	t.Fatal("a feed cut short was not aborted")
}

// TestFeedFailsFirst: a failure on the first page is an uncached 503; a
// store without products has an empty feed.
func TestFeedFailsFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	f.catalog.listErr = errors.New("database down")
	if rec := f.get(t, "/feeds/google.xml?key="+feedKey); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("first page failed: %d %v", rec.Code, rec.Header())
	}
	f = newFixture(t, withFeeds(fakeFeeds{key: feedKey}))
	f.catalog.products = nil
	rec := f.get(t, "/feeds/google.xml?key="+feedKey)
	if rec.Code != http.StatusOK || len(parseFeed(t, rec.Body.Bytes()).Channel.Items) != 0 {
		t.Fatalf("an empty store's feed: %d %s", rec.Code, rec.Body)
	}
}
