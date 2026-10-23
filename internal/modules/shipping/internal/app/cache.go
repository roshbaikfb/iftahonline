package app

import (
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// CacheConfig bounds the in-memory cache of store configs (GOMEMLIMIT
// rule: nothing unbounded).
type CacheConfig struct {
	TTL    time.Duration // how stale a config may be when an invalidation is lost
	Stores int           // configs kept
}

// DefaultCacheConfig keeps every active store's config (a few KiB each)
// for 30 s.
func DefaultCacheConfig() CacheConfig { return CacheConfig{TTL: 30 * time.Second, Stores: 5000} }

// configs caches store configs by generation: invalidate moves a store
// to a new generation, so a load that started before the change can never
// be served after it (ADR-0018's scheme).
type configs struct {
	lru  *expirable.LRU[string, domain.Config]
	mu   sync.Mutex
	gens map[tenant.StoreID]uint64 // only stores invalidated since start
}

func newConfigs(c CacheConfig) *configs {
	return &configs{lru: expirable.NewLRU[string, domain.Config](c.Stores, nil, c.TTL), gens: map[tenant.StoreID]uint64{}}
}

func (c *configs) key(store tenant.StoreID) string {
	c.mu.Lock()
	g := c.gens[store]
	c.mu.Unlock()
	return store.String() + "|" + strconv.FormatUint(g, 10)
}

func (c *configs) get(store tenant.StoreID) (domain.Config, string, bool) {
	k := c.key(store)
	cfg, ok := c.lru.Get(k)
	return cfg, k, ok
}

func (c *configs) put(key string, cfg domain.Config) { c.lru.Add(key, cfg) }

func (c *configs) invalidate(store tenant.StoreID) {
	c.mu.Lock()
	c.gens[store]++
	c.mu.Unlock()
}
