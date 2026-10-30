package storecache_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/storecache"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestCache(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	a, b := tenant.StoreID{ID: ids.New()}, tenant.StoreID{ID: ids.New()}
	c := storecache.New[int](storecache.Config{TTL: time.Minute, Stores: 2})

	_, k, ok := c.Get(a)
	if ok {
		t.Fatal("hit on an empty cache")
	}
	c.Put(k, 1)
	if v, _, ok := c.Get(a); !ok || v != 1 {
		t.Fatalf("Get = %d, %v", v, ok)
	}
	if _, _, ok := c.Get(b); ok {
		t.Fatal("store B got A's value")
	}

	// A load that began before an invalidation is never served after it.
	_, inFlight, _ := c.Get(b)
	c.Invalidate(b)
	c.Put(inFlight, 7)
	if _, _, ok := c.Get(b); ok {
		t.Fatal("a stale load was served after the invalidation")
	}
	c.Invalidate(a)
	if _, _, ok := c.Get(a); ok {
		t.Fatal("invalidated value served")
	}
}

func TestCacheExpires(t *testing.T) {
	t.Parallel()
	a := tenant.StoreID{ID: ulid.NewGenerator().New()}
	c := storecache.New[string](storecache.Config{TTL: 20 * time.Millisecond, Stores: 10})
	_, k, _ := c.Get(a)
	c.Put(k, "x")
	time.Sleep(100 * time.Millisecond)
	if _, _, ok := c.Get(a); ok {
		t.Fatal("value outlived its TTL")
	}
}
