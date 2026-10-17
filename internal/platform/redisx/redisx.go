// Package redisx builds the go-redis client used for caching and rate
// limits.
package redisx

import (
	"fmt"
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
