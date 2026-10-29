package app_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
)

// TestAddedStore (ADR-0043): a pending AddToCart is read once, is the
// cart's in its own store only, and expires.
func TestAddedStore(t *testing.T) {
	t.Parallel()
	f, _ := newCartFixture(t)
	added := store.NewAdded(f.rdb)
	hash := []byte("token-hash-of-a-cart")
	if err := added.Put(f.ctxA, hash, "01M5PAGE000000000000000001.01M5PAGE000000000000000002.2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := added.Take(f.ctxB, hash); err != nil || ok {
		t.Fatalf("another store took it: %v %v", ok, err)
	}
	v, ok, err := added.Take(f.ctxA, hash)
	if err != nil || !ok || v != "01M5PAGE000000000000000001.01M5PAGE000000000000000002.2" {
		t.Fatalf("take = %q %v %v", v, ok, err)
	}
	if _, ok, err = added.Take(f.ctxA, hash); err != nil || ok {
		t.Fatalf("taken twice: %v %v", ok, err)
	}
	if err = added.Put(f.ctxA, hash, "x", time.Minute); err != nil {
		t.Fatal(err)
	}
	keys, err := f.rdb.Keys(f.ctxA, "sf:px-add:*").Result()
	if err != nil || len(keys) == 0 {
		t.Fatalf("keys %v %v", keys, err)
	}
	for _, k := range keys {
		if ttl := f.rdb.TTL(f.ctxA, k).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("%s ttl %v", k, ttl)
		}
	}
}
