// Package app holds the tenancy use cases: store lifecycle, custom
// domains and their verification, and host → store resolution.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
)

// ErrUnknownHost means no active-or-not store is reachable at the host.
var ErrUnknownHost = errors.New("tenancy: unknown host")

// Directory is the authoritative host lookup (PostgreSQL).
type Directory interface {
	ResolveHost(ctx context.Context, host string) (res domain.Resolution, found bool, err error)
}

// SharedCache is the cache shared by all instances (Redis). Get reports
// cached=false on a miss; a cached negative entry is cached=true,
// found=false.
type SharedCache interface {
	Get(ctx context.Context, host string) (res domain.Resolution, found, cached bool, err error)
	Set(ctx context.Context, host string, res domain.Resolution, found bool) error
}

// ResolverConfig sizes the in-process caches (GOMEMLIMIT rule: bounded).
type ResolverConfig struct {
	Size        int
	TTL         time.Duration
	NegSize     int
	NegTTL      time.Duration
	LoadTimeout time.Duration
}

// Resolver maps hosts to stores through L1 (in-process LRU, separate
// negative LRU), L2 (SharedCache) and L3 (Directory), collapsing
// concurrent misses per host (ADR-0004).
type Resolver struct {
	pos    *expirable.LRU[string, domain.Resolution]
	neg    *expirable.LRU[string, struct{}]
	shared SharedCache
	dir    Directory
	log    *slog.Logger
	sf     singleflight.Group
	cfg    ResolverConfig
}

// NewResolver validates cfg and builds a resolver.
func NewResolver(cfg ResolverConfig, shared SharedCache, dir Directory, log *slog.Logger) (*Resolver, error) {
	if cfg.Size < 1 || cfg.NegSize < 1 || cfg.TTL <= 0 || cfg.NegTTL <= 0 || cfg.LoadTimeout <= 0 {
		return nil, fmt.Errorf("tenancy: invalid resolver config %+v", cfg)
	}
	return &Resolver{
		pos:    expirable.NewLRU[string, domain.Resolution](cfg.Size, nil, cfg.TTL),
		neg:    expirable.NewLRU[string, struct{}](cfg.NegSize, nil, cfg.NegTTL),
		shared: shared, dir: dir, log: log, cfg: cfg,
	}, nil
}

// Resolve returns the store for a raw Host header. Invalid and unknown
// hosts are ErrUnknownHost. An L1 hit touches neither Redis nor the
// database.
func (r *Resolver) Resolve(ctx context.Context, rawHost string) (domain.Resolution, error) {
	host, err := domain.NormalizeHost(rawHost)
	if err != nil {
		return domain.Resolution{}, ErrUnknownHost
	}
	if res, ok := r.pos.Get(host); ok {
		return res, nil
	}
	if r.neg.Contains(host) {
		return domain.Resolution{}, ErrUnknownHost
	}

	v, err, _ := r.sf.Do(host, func() (any, error) {
		// The load is shared by every waiter, so it must not die with the
		// first caller's request.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.LoadTimeout)
		defer cancel()
		return r.load(lctx, host)
	})
	if err != nil {
		return domain.Resolution{}, err
	}
	res, ok := v.(lookup)
	if !ok || !res.found {
		return domain.Resolution{}, ErrUnknownHost
	}
	return res.res, nil
}

type lookup struct {
	res   domain.Resolution
	found bool
}

func (r *Resolver) load(ctx context.Context, host string) (lookup, error) {
	res, found, cached, err := r.shared.Get(ctx, host)
	switch {
	case err != nil:
		// Redis is an optimization; the database still answers.
		r.log.WarnContext(ctx, "tenancy: shared host cache unavailable", "err", err)
	case cached:
		r.remember(host, res, found)
		return lookup{res, found}, nil
	}

	res, found, err = r.dir.ResolveHost(ctx, host)
	if err != nil {
		return lookup{}, fmt.Errorf("tenancy: resolve %s: %w", host, err)
	}
	r.remember(host, res, found)
	if err := r.shared.Set(ctx, host, res, found); err != nil {
		r.log.WarnContext(ctx, "tenancy: shared host cache set failed", "err", err)
	}
	return lookup{res, found}, nil
}

func (r *Resolver) remember(host string, res domain.Resolution, found bool) {
	if found {
		r.neg.Remove(host)
		r.pos.Add(host, res)
		return
	}
	r.pos.Remove(host)
	r.neg.Add(host, struct{}{})
}

// Invalidate drops hosts from this instance's L1 caches.
func (r *Resolver) Invalidate(hosts ...string) {
	for _, h := range hosts {
		r.pos.Remove(h)
		r.neg.Remove(h)
	}
}

// Listen evicts hosts received on msgs until ctx ends or msgs closes. Each
// message is the list of hosts one change affected.
func (r *Resolver) Listen(ctx context.Context, msgs <-chan []string) {
	for {
		select {
		case <-ctx.Done():
			return
		case hosts, ok := <-msgs:
			if !ok {
				return
			}
			r.Invalidate(hosts...)
		}
	}
}
