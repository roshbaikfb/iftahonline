package web

import (
	"strconv"
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
	tags      string // Cache-Tag header
	language  Locale
	challenge bool // the page shows a Turnstile challenge (CSP)
}

// caches hold rendered pages and store views. Entries are keyed by the
// store's generation: Invalidate moves a store to a new generation, so
// its old entries are never served again and age out. The TTL bounds
// staleness when an invalidation message is lost (ADR-0018).
type caches struct {
	cfg   CacheConfig
	pages *expirable.LRU[string, cached]
	views *expirable.LRU[string, *storeView]

	mu   sync.Mutex
	gens map[tenant.StoreID]uint64 // only stores invalidated since start: bounded by the number of stores
}

func newCaches(cfg CacheConfig) *caches {
	return &caches{
		cfg:   cfg,
		pages: expirable.NewLRU[string, cached](cfg.Pages, nil, cfg.TTL),
		views: expirable.NewLRU[string, *storeView](cfg.Views, nil, cfg.TTL),
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
