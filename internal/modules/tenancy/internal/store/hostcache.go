package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// InvalidateChannel carries newline-separated hosts whose cached
// resolution must be dropped by every instance.
const InvalidateChannel = "tenancy:invalidate"

const (
	hostKeyPrefix = "tenancy:host:"
	negativeValue = "-"
)

// HostCache is the Redis (L2) host cache.
type HostCache struct {
	rdb    *redis.Client
	ttl    time.Duration
	negTTL time.Duration
}

// NewHostCache returns a cache keeping found hosts for ttl and unknown
// hosts for negTTL.
func NewHostCache(rdb *redis.Client, ttl, negTTL time.Duration) *HostCache {
	return &HostCache{rdb: rdb, ttl: ttl, negTTL: negTTL}
}

// Get implements app.SharedCache.
func (c *HostCache) Get(ctx context.Context, host string) (res domain.Resolution, found, cached bool, err error) {
	v, err := c.rdb.Get(ctx, hostKeyPrefix+host).Result()
	if errors.Is(err, redis.Nil) {
		return domain.Resolution{}, false, false, nil
	}
	if err != nil {
		return domain.Resolution{}, false, false, fmt.Errorf("tenancy hostcache: get: %w", err)
	}
	if v == negativeValue {
		return domain.Resolution{}, false, true, nil
	}
	idStr, statusStr, ok := strings.Cut(v, ":")
	id, idErr := tenant.ParseStoreID(idStr)
	status, stOK := domain.ParseStoreStatus(statusStr)
	if !ok || idErr != nil || !stOK {
		// Corrupt entry: treat as a miss so the database answers.
		return domain.Resolution{}, false, false, nil
	}
	return domain.Resolution{StoreID: id, Status: status}, true, true, nil
}

// Set implements app.SharedCache.
func (c *HostCache) Set(ctx context.Context, host string, res domain.Resolution, found bool) error {
	v, ttl := negativeValue, c.negTTL
	if found {
		v, ttl = res.StoreID.String()+":"+string(res.Status), c.ttl
	}
	if err := c.rdb.Set(ctx, hostKeyPrefix+host, v, ttl).Err(); err != nil {
		return fmt.Errorf("tenancy hostcache: set: %w", err)
	}
	return nil
}

// Invalidate deletes hosts from Redis and tells every instance to drop
// them from L1.
func (c *HostCache) Invalidate(ctx context.Context, hosts []string) error {
	if len(hosts) == 0 {
		return nil
	}
	keys := make([]string, len(hosts))
	for i, h := range hosts {
		keys[i] = hostKeyPrefix + h
	}
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("tenancy hostcache: delete: %w", err)
	}
	if err := c.rdb.Publish(ctx, InvalidateChannel, strings.Join(hosts, "\n")).Err(); err != nil {
		return fmt.Errorf("tenancy hostcache: publish: %w", err)
	}
	return nil
}

// Subscribe streams invalidated host lists until ctx ends (see
// redisx.Subscribe); the L1 TTL bounds what a lost message costs.
func (c *HostCache) Subscribe(ctx context.Context, log *slog.Logger) <-chan []string {
	in := redisx.Subscribe(ctx, c.rdb, InvalidateChannel, log)
	out := make(chan []string, 64)
	go func() {
		defer close(out)
		for payload := range in {
			select {
			case out <- strings.Split(payload, "\n"):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Directory adapts ResolveHost to app.Directory on a pool or connection.
type Directory struct{ DB Tx }

// ResolveHost implements app.Directory.
func (d Directory) ResolveHost(ctx context.Context, host string) (domain.Resolution, bool, error) {
	return ResolveHost(ctx, d.DB, host)
}
