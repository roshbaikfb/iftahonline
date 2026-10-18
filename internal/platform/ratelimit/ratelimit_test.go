package ratelimit_test

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func limiter(t *testing.T) (*ratelimit.Limiter, *redis.Client) {
	t.Helper()
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	l, err := ratelimit.New(rdb, "rl-test")
	if err != nil {
		t.Fatal(err)
	}
	return l, rdb
}

// key returns a fresh key so parallel tests never share state.
func key() string { return ulid.NewGenerator().New().String() }

func TestNewValidatesPrefix(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "a b"} {
		if _, err := ratelimit.New(nil, p); err == nil {
			t.Fatalf("prefix %q must be rejected", p)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	t.Parallel()
	l, _ := limiter(t)
	ok := ratelimit.Limit{Burst: 2, Period: time.Minute}
	tests := []struct {
		name string
		lim  ratelimit.Limit
		n    int
	}{
		{"zero burst", ratelimit.Limit{Burst: 0, Period: time.Minute}, 1},
		{"tiny period", ratelimit.Limit{Burst: 1, Period: time.Microsecond}, 1},
		{"negative n", ok, -1},
		{"n above burst", ok, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := l.AllowN(t.Context(), key(), tt.lim, tt.n); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestBurstThenDeny(t *testing.T) {
	t.Parallel()
	l, _ := limiter(t)
	ctx, k := t.Context(), key()
	lim := ratelimit.Limit{Burst: 3, Period: time.Hour}

	for i := range 3 {
		r, err := l.Allow(ctx, k, lim)
		if err != nil || !r.Allowed {
			t.Fatalf("event %d: %+v, %v", i, r, err)
		}
		if r.Remaining != 2-i {
			t.Fatalf("event %d: remaining = %d", i, r.Remaining)
		}
	}
	r, err := l.Allow(ctx, k, lim)
	if err != nil || r.Allowed {
		t.Fatalf("4th event: %+v, %v", r, err)
	}
	// One event refills every 20 minutes.
	if r.RetryAfter <= 19*time.Minute || r.RetryAfter > 20*time.Minute {
		t.Fatalf("retry after = %v", r.RetryAfter)
	}
	if r.Remaining != 0 {
		t.Fatalf("remaining = %d", r.Remaining)
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	t.Parallel()
	l, _ := limiter(t)
	ctx, k := t.Context(), key()
	lim := ratelimit.Limit{Burst: 1, Period: time.Hour}

	for range 3 {
		r, err := l.Peek(ctx, k, lim)
		if err != nil || !r.Allowed || r.Remaining != 1 {
			t.Fatalf("peek on fresh key: %+v, %v", r, err)
		}
	}
	if r, err := l.Allow(ctx, k, lim); err != nil || !r.Allowed {
		t.Fatalf("allow: %+v, %v", r, err)
	}
	r, err := l.Peek(ctx, k, lim)
	if err != nil || r.Allowed || r.RetryAfter <= 0 {
		t.Fatalf("peek after exhaustion: %+v, %v", r, err)
	}
}

func TestRefillAndReset(t *testing.T) {
	t.Parallel()
	l, rdb := limiter(t)
	ctx, k := t.Context(), key()
	lim := ratelimit.Limit{Burst: 1, Period: 200 * time.Millisecond}

	if r, _ := l.Allow(ctx, k, lim); !r.Allowed {
		t.Fatal("first must pass")
	}
	if r, _ := l.Allow(ctx, k, lim); r.Allowed {
		t.Fatal("second must be denied")
	}
	time.Sleep(250 * time.Millisecond)
	if r, _ := l.Allow(ctx, k, lim); !r.Allowed {
		t.Fatal("must refill after the period")
	}

	if err := l.Reset(ctx, k); err != nil {
		t.Fatal(err)
	}
	if r, _ := l.Allow(ctx, k, lim); !r.Allowed {
		t.Fatal("must pass after reset")
	}
	// Keys expire on their own once fully refilled.
	ttl, err := rdb.PTTL(ctx, "rl-test:"+k).Result()
	if err != nil || ttl <= 0 || ttl > 300*time.Millisecond {
		t.Fatalf("ttl = %v, %v", ttl, err)
	}
}

// TestConcurrentExactness: under contention exactly Burst events pass.
func TestConcurrentExactness(t *testing.T) {
	t.Parallel()
	l, _ := limiter(t)
	ctx, k := t.Context(), key()
	lim := ratelimit.Limit{Burst: 10, Period: time.Hour}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			r, err := l.Allow(ctx, k, lim)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed = %d, want 10", allowed.Load())
	}
}

func TestRedisErrorIsReturned(t *testing.T) {
	t.Parallel()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	l, err := ratelimit.New(rdb, "rl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Allow(t.Context(), "k", ratelimit.Limit{Burst: 1, Period: time.Second}); err == nil {
		t.Fatal("want error when Redis is down")
	}
	if err := l.Reset(t.Context(), "k"); err == nil {
		t.Fatal("want error when Redis is down")
	}
}
