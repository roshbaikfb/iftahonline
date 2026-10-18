package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeDir is an in-memory Directory that counts lookups.
type fakeDir struct {
	mu    sync.Mutex
	hosts map[string]domain.Resolution
	err   error
	calls atomic.Int64
	delay time.Duration
}

func (d *fakeDir) ResolveHost(ctx context.Context, host string) (domain.Resolution, bool, error) {
	d.calls.Add(1)
	if d.delay > 0 {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			return domain.Resolution{}, false, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return domain.Resolution{}, false, d.err
	}
	r, ok := d.hosts[host]
	return r, ok, nil
}

func (d *fakeDir) set(host string, r domain.Resolution) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hosts[host] = r
}

// fakeShared is an in-memory SharedCache that counts calls.
type fakeShared struct {
	mu      sync.Mutex
	entries map[string]*domain.Resolution // nil value = cached negative
	err     error
	gets    atomic.Int64
	sets    atomic.Int64
}

func (s *fakeShared) Get(_ context.Context, host string) (domain.Resolution, bool, bool, error) {
	s.gets.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return domain.Resolution{}, false, false, s.err
	}
	e, ok := s.entries[host]
	if !ok {
		return domain.Resolution{}, false, false, nil
	}
	if e == nil {
		return domain.Resolution{}, false, true, nil
	}
	return *e, true, true, nil
}

func (s *fakeShared) Set(_ context.Context, host string, r domain.Resolution, found bool) error {
	s.sets.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if found {
		s.entries[host] = &r
	} else {
		s.entries[host] = nil
	}
	return nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testConfig() app.ResolverConfig {
	return app.ResolverConfig{Size: 100, TTL: time.Minute, NegSize: 10, NegTTL: time.Minute, LoadTimeout: time.Second}
}

func newResolver(t testing.TB, cfg app.ResolverConfig) (*app.Resolver, *fakeDir, *fakeShared, domain.Resolution) {
	t.Helper()
	res := domain.Resolution{StoreID: tenant.StoreID{ID: ulid.NewGenerator().New()}, Status: domain.StatusActive}
	dir := &fakeDir{hosts: map[string]domain.Resolution{"shop.iftahonline.com": res}}
	shared := &fakeShared{entries: map[string]*domain.Resolution{}}
	r, err := app.NewResolver(cfg, shared, dir, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	return r, dir, shared, res
}

func TestNewResolverValidatesConfig(t *testing.T) {
	t.Parallel()
	for _, bad := range []app.ResolverConfig{
		{}, {Size: 1, NegSize: 1, TTL: time.Second, NegTTL: time.Second},
		{Size: 0, NegSize: 1, TTL: time.Second, NegTTL: time.Second, LoadTimeout: time.Second},
	} {
		if _, err := app.NewResolver(bad, nil, nil, quietLog()); err == nil {
			t.Fatalf("config %+v must be rejected", bad)
		}
	}
}

// TestCacheHitDoesNoIO is the acceptance check: after the first lookup,
// resolving the same host (in any spelling) touches neither the shared
// cache nor the directory.
func TestCacheHitDoesNoIO(t *testing.T) {
	t.Parallel()
	r, dir, shared, want := newResolver(t, testConfig())
	ctx := t.Context()

	got, err := r.Resolve(ctx, "shop.iftahonline.com")
	if err != nil || got != want {
		t.Fatalf("first = %v, %v", got, err)
	}
	if dir.calls.Load() != 1 || shared.gets.Load() != 1 || shared.sets.Load() != 1 {
		t.Fatalf("miss: dir=%d gets=%d sets=%d", dir.calls.Load(), shared.gets.Load(), shared.sets.Load())
	}
	for _, host := range []string{"shop.iftahonline.com", "SHOP.iftahonline.com:443", "shop.iftahonline.com."} {
		if got, err := r.Resolve(ctx, host); err != nil || got != want {
			t.Fatalf("%s = %v, %v", host, got, err)
		}
	}
	if dir.calls.Load() != 1 || shared.gets.Load() != 1 {
		t.Fatalf("hits did I/O: dir=%d gets=%d", dir.calls.Load(), shared.gets.Load())
	}
}

func TestUnknownAndInvalidHosts(t *testing.T) {
	t.Parallel()
	r, dir, shared, _ := newResolver(t, testConfig())
	ctx := t.Context()

	for range 3 {
		if _, err := r.Resolve(ctx, "nope.iftahonline.com"); !errors.Is(err, app.ErrUnknownHost) {
			t.Fatalf("err = %v", err)
		}
	}
	if dir.calls.Load() != 1 {
		t.Fatalf("unknown host must be negatively cached; dir calls = %d", dir.calls.Load())
	}
	if e, ok := shared.entries["nope.iftahonline.com"]; !ok || e != nil {
		t.Fatal("negative entry must reach the shared cache")
	}
	before := dir.calls.Load()
	for _, bad := range []string{"", "127.0.0.1", "[::1]:80", "a..b", "x.com:abc"} {
		if _, err := r.Resolve(ctx, bad); !errors.Is(err, app.ErrUnknownHost) {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}
	if dir.calls.Load() != before {
		t.Fatal("invalid hosts must not reach the directory")
	}
}

// TestNegativeFloodKeepsRealStores: a burst of random unknown hosts fills
// only the negative cache, so a known store stays an L1 hit.
func TestNegativeFloodKeepsRealStores(t *testing.T) {
	t.Parallel()
	r, dir, _, _ := newResolver(t, testConfig())
	ctx := t.Context()
	if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil {
		t.Fatal(err)
	}
	gen := ulid.NewGenerator()
	for range 500 {
		_, _ = r.Resolve(ctx, "x"+gen.New().String()+".example.com")
	}
	before := dir.calls.Load()
	if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil || dir.calls.Load() != before {
		t.Fatalf("real store evicted by flood: err=%v calls %d -> %d", err, before, dir.calls.Load())
	}
}

func TestSharedCacheLayer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("L2 hit skips the directory", func(t *testing.T) {
		t.Parallel()
		r, dir, shared, want := newResolver(t, testConfig())
		shared.entries["other.example.com"] = &want
		shared.entries["gone.example.com"] = nil
		if got, err := r.Resolve(ctx, "other.example.com"); err != nil || got != want {
			t.Fatalf("got %v, %v", got, err)
		}
		if _, err := r.Resolve(ctx, "gone.example.com"); !errors.Is(err, app.ErrUnknownHost) {
			t.Fatalf("cached negative: err = %v", err)
		}
		if dir.calls.Load() != 0 {
			t.Fatalf("dir calls = %d", dir.calls.Load())
		}
	})

	t.Run("Redis down falls back to the directory", func(t *testing.T) {
		t.Parallel()
		r, dir, shared, want := newResolver(t, testConfig())
		shared.err = errors.New("redis down")
		if got, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil || got != want {
			t.Fatalf("got %v, %v", got, err)
		}
		if dir.calls.Load() != 1 {
			t.Fatalf("dir calls = %d", dir.calls.Load())
		}
	})

	t.Run("directory error is returned and not cached", func(t *testing.T) {
		t.Parallel()
		r, dir, _, want := newResolver(t, testConfig())
		dir.err = errors.New("db down")
		if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err == nil || errors.Is(err, app.ErrUnknownHost) {
			t.Fatalf("err = %v, want a non-unknown error", err)
		}
		dir.err = nil
		if got, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil || got != want {
			t.Fatalf("after recovery: %v, %v", got, err)
		}
	})
}

// TestConcurrentMissesCollapse: many simultaneous requests for one cold
// host cause a single directory lookup.
func TestConcurrentMissesCollapse(t *testing.T) {
	t.Parallel()
	r, dir, _, want := newResolver(t, testConfig())
	dir.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if got, err := r.Resolve(t.Context(), "shop.iftahonline.com"); err != nil || got != want {
				t.Errorf("got %v, %v", got, err)
			}
		})
	}
	wg.Wait()
	if dir.calls.Load() != 1 {
		t.Fatalf("dir calls = %d, want 1", dir.calls.Load())
	}
}

// TestLoadSurvivesCallerCancel: the shared load must not fail for every
// waiter because the first caller went away.
func TestLoadSurvivesCallerCancel(t *testing.T) {
	t.Parallel()
	r, dir, _, want := newResolver(t, testConfig())
	dir.delay = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil || got != want {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestInvalidateAndListen(t *testing.T) {
	t.Parallel()
	r, dir, shared, want := newResolver(t, testConfig())
	ctx := t.Context()
	if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, "new.iftahonline.com"); !errors.Is(err, app.ErrUnknownHost) {
		t.Fatal(err)
	}

	// The store is suspended and a new store takes "new"; the database
	// and shared cache change, then the invalidation arrives.
	suspended := domain.Resolution{StoreID: want.StoreID, Status: domain.StatusSuspended}
	dir.set("shop.iftahonline.com", suspended)
	dir.set("new.iftahonline.com", want)
	shared.mu.Lock()
	clear(shared.entries)
	shared.mu.Unlock()

	msgs := make(chan []string)
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.Listen(lctx, msgs); close(done) }()
	msgs <- []string{"shop.iftahonline.com", "new.iftahonline.com"}
	cancel()
	<-done

	if got, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil || got != suspended {
		t.Fatalf("after invalidation: %v, %v", got, err)
	}
	if got, err := r.Resolve(ctx, "new.iftahonline.com"); err != nil || got != want {
		t.Fatalf("negative entry must be dropped: %v, %v", got, err)
	}

	// Listen also returns when the channel closes.
	closed := make(chan []string)
	close(closed)
	r.Listen(ctx, closed)
}

func TestL1Expires(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.TTL = 20 * time.Millisecond
	r, dir, shared, _ := newResolver(t, cfg)
	if _, err := r.Resolve(t.Context(), "shop.iftahonline.com"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	shared.mu.Lock()
	clear(shared.entries)
	shared.mu.Unlock()
	if _, err := r.Resolve(t.Context(), "shop.iftahonline.com"); err != nil {
		t.Fatal(err)
	}
	if dir.calls.Load() != 2 {
		t.Fatalf("expired entry must be reloaded; dir calls = %d", dir.calls.Load())
	}
}

func BenchmarkResolveL1Hit(b *testing.B) {
	r, _, _, _ := newResolver(b, testConfig())
	ctx := b.Context()
	if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Resolve(ctx, "shop.iftahonline.com:443"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveL1HitParallel(b *testing.B) {
	r, _, _, _ := newResolver(b, testConfig())
	ctx := b.Context()
	if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.Resolve(ctx, "shop.iftahonline.com"); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkResolveNegativeHit(b *testing.B) {
	r, _, _, _ := newResolver(b, testConfig())
	ctx := b.Context()
	_, _ = r.Resolve(ctx, "nope.example.com")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Resolve(ctx, "nope.example.com"); !errors.Is(err, app.ErrUnknownHost) {
			b.Fatal(err)
		}
	}
}
