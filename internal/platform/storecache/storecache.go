// Package storecache is a bounded, expiring in-memory cache of one value
// per store, with invalidation by generation (ADR-0018's scheme).
//
// Invalidate moves a store to a new generation. A load that started
// before the change carries the old generation's key, so what it puts is
// never served after the change. Shipping caches store configs with it,
// entitlements store grants.
package storecache

import (
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Config bounds the cache (GOMEMLIMIT rule: nothing unbounded).
type Config struct {
	TTL    time.Duration // how stale a value may be when an invalidation is lost
	Stores int           // values kept
}

// Key is a store's key in its current generation, from Get. A struct,
// not a string, so a lookup allocates nothing.
type Key struct {
	store tenant.StoreID
	gen   uint64
}

// Cache holds one V per store.
type Cache[V any] struct {
	lru  *expirable.LRU[Key, V]
	mu   sync.Mutex
	gens map[tenant.StoreID]uint64 // only stores invalidated since start: bounded by the number of stores
}

// New builds a cache.
func New[V any](c Config) *Cache[V] {
	return &Cache[V]{lru: expirable.NewLRU[Key, V](c.Stores, nil, c.TTL), gens: map[tenant.StoreID]uint64{}}
}

func (c *Cache[V]) key(store tenant.StoreID) Key {
	c.mu.Lock()
	g := c.gens[store]
	c.mu.Unlock()
	return Key{store: store, gen: g}
}

// Get returns the store's value, and the key to Put a freshly loaded one
// under when ok is false.
func (c *Cache[V]) Get(store tenant.StoreID) (v V, k Key, ok bool) {
	k = c.key(store)
	v, ok = c.lru.Get(k)
	return v, k, ok
}

// Put stores a value under the key Get returned before loading it.
func (c *Cache[V]) Put(k Key, v V) { c.lru.Add(k, v) }

// Invalidate makes the store's cached value, and any load in flight,
// stale.
func (c *Cache[V]) Invalidate(store tenant.StoreID) {
	c.mu.Lock()
	c.gens[store]++
	c.mu.Unlock()
}
