package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// CacheConfig bounds the in-memory caches (GOMEMLIMIT rule: nothing
// unbounded).
type CacheConfig struct {
	TTL         time.Duration // how stale a cached page or store view may be; ≤ 30s keeps the 60s promise
	Pages       int           // rendered pages kept
	MaxPageSize int           // larger pages are rendered every time
	Views       int           // store views kept (one per active store)
}

// DefaultCacheConfig suits one storefront instance in front of the edge
// cache: about 64 MiB of pages at 32 KiB each.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{TTL: 30 * time.Second, Pages: 2000, MaxPageSize: 256 << 10, Views: 5000}
}

// cached is a rendered response: everything the edge needs to serve it
// again.
type cached struct {
	status    int
	body      []byte
	styleHash string
	pageStyle string // the CSP hash of the page's own style; empty for none
	tags      string // Cache-Tag header
	language  Locale
	challenge bool   // the page shows a Turnstile challenge (CSP)
	gz        []byte // body, gzip-compressed once when the page is cached
}

// caches hold rendered pages and store views. Entries are keyed by the
// store's generation: Invalidate moves a store to a new generation, so
// its old entries are never served again and age out. The TTL bounds
// staleness when an invalidation message is lost (ADR-0018).
type caches struct {
	cfg   CacheConfig
	pages *expirable.LRU[string, cached]
	views *expirable.LRU[string, *storeView]
	cards *expirable.LRU[string, bool] // whether a store takes cards (checkout)

	mu   sync.Mutex
	gens map[tenant.StoreID]uint64 // only stores invalidated since start: bounded by the number of stores
}

func newCaches(cfg CacheConfig) *caches {
	return &caches{
		cfg:   cfg,
		pages: expirable.NewLRU[string, cached](cfg.Pages, nil, cfg.TTL),
		views: expirable.NewLRU[string, *storeView](cfg.Views, nil, cfg.TTL),
		cards: expirable.NewLRU[string, bool](cfg.Views, nil, cfg.TTL),
		gens:  map[tenant.StoreID]uint64{},
	}
}

// Invalidate drops everything cached for store.
func (c *caches) Invalidate(store tenant.StoreID) {
	c.mu.Lock()
	c.gens[store]++
	c.mu.Unlock()
}

func (c *caches) prefix(store tenant.StoreID) string {
	c.mu.Lock()
	g := c.gens[store]
	c.mu.Unlock()
	return store.String() + "|" + strconv.FormatUint(g, 10) + "|"
}

func (c *caches) view(store tenant.StoreID) (*storeView, string, bool) {
	p := c.prefix(store)
	v, ok := c.views.Get(p)
	return v, p, ok
}

func (c *caches) page(prefix, key string) (cached, bool) { return c.pages.Get(prefix + key) }

func (c *caches) putPage(prefix, key string, p cached) {
	if len(p.body) <= c.cfg.MaxPageSize {
		c.pages.Add(prefix+key, p)
	}
}

// gzipWriters are reused: pages are compressed on every cache miss.
var gzipWriters = sync.Pool{New: func() any { //nolint:gochecknoglobals // a pool of reusable writers, no state
	w, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	if err != nil {
		return nil // never: BestSpeed is a valid level; compress then sends the page plain
	}
	return w
}}

// compress gzips a page body. Pages are small (tens of KiB) and mostly
// repeated markup, so BestSpeed already shrinks them severalfold.
func compress(body []byte) []byte {
	zw, ok := gzipWriters.Get().(*gzip.Writer)
	if !ok {
		return nil
	}
	defer gzipWriters.Put(zw)
	var buf bytes.Buffer
	buf.Grow(len(body) / 3)
	zw.Reset(&buf)
	if _, err := zw.Write(body); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// acceptsGzip reports whether the client takes a gzip body.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return strings.TrimSpace(q) != "q=0"
		}
	}
	return false
}
