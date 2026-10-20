// Package redisx builds the go-redis client used for caching and rate
// limits.
package redisx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
)

// New returns a client with tight timeouts. It connects lazily.
func New(c config.Redis) (*redis.Client, error) {
	opts, err := redis.ParseURL(c.URL)
	if err != nil {
		return nil, fmt.Errorf("redisx: parse url: %w", err)
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 500 * time.Millisecond
	opts.WriteTimeout = 500 * time.Millisecond
	opts.PoolTimeout = time.Second
	opts.MaxRetries = 2
	return redis.NewClient(opts), nil
}

// Subscribe streams the payloads published on channel until ctx ends;
// the returned channel is closed then. go-redis reconnects (and
// resubscribes) on its own. Messages published while disconnected are
// lost, so subscribers must also expire what they cache (a TTL).
func Subscribe(ctx context.Context, rdb *redis.Client, channel string, log *slog.Logger) <-chan string {
	out := make(chan string, 64)
	ps := rdb.Subscribe(ctx, channel)
	go func() {
		defer close(out)
		defer func() {
			if err := ps.Close(); err != nil {
				log.DebugContext(ctx, "redisx: close subscription", "channel", channel, "err", err)
			}
		}()
		ch := ps.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- m.Payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
