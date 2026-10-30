package app

import (
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/storecache"
)

// CacheConfig bounds the in-memory cache of store configs (GOMEMLIMIT
// rule: nothing unbounded).
type CacheConfig = storecache.Config

// DefaultCacheConfig keeps every active store's config (a few KiB each)
// for 30 s.
func DefaultCacheConfig() CacheConfig { return CacheConfig{TTL: 30 * time.Second, Stores: 5000} }
