// Package app holds the entitlement use cases (ADR-0038, ADR-0045):
// whether the store in ctx has a feature, whether it may hold one more of
// a counted one, and the monthly quotas: consume units in the caller's
// transaction, give them back, read the month's usage.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/storecache"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Source says what the store in ctx is granted, reading in tx: the
// billing module, from the store's plan.
type Source interface {
	GrantsTx(ctx context.Context, tx pgx.Tx) (domain.Grants, error)
}

// Fixed grants every store the same (tests and demo tools).
type Fixed domain.Grants

// GrantsTx implements Source.
func (f Fixed) GrantsTx(context.Context, pgx.Tx) (domain.Grants, error) { return domain.Grants(f), nil }

// DefaultCacheConfig keeps every active store's grants (well under 1 KiB
// each) for 30 s.
func DefaultCacheConfig() storecache.Config {
	return storecache.Config{TTL: 30 * time.Second, Stores: 10000}
}

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	source Source
	cache  *storecache.Cache[domain.Grants]
	now    func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	Source Source
	// Cache bounds the grants kept in memory; zero is DefaultCacheConfig.
	Cache storecache.Config
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	if d.Cache == (storecache.Config{}) {
		d.Cache = DefaultCacheConfig()
	}
	return &Service{db: d.DB, source: d.Source, cache: storecache.New[domain.Grants](d.Cache), now: now}
}

// Invalidate drops the store's cached grants, so the next check reads
// its plan again.
func (s *Service) Invalidate(store tenant.StoreID) { s.cache.Invalidate(store) }

// grantsTx are the store-in-ctx's grants, from the cache or read in tx.
func (s *Service) grantsTx(ctx context.Context, tx pgx.Tx) (domain.Grants, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("entitlements: %w", err)
	}
	g, key, ok := s.cache.Get(id)
	if ok {
		return g, nil
	}
	if g, err = s.source.GrantsTx(ctx, tx); err != nil {
		return nil, fmt.Errorf("entitlements: grants: %w", err)
	}
	s.cache.Put(key, g)
	return g, nil
}

// grant is the store-in-ctx's grant of f. A cache hit costs no query and
// no transaction.
func (s *Service) grant(ctx context.Context, f domain.Feature) (domain.Grant, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Grant{}, fmt.Errorf("entitlements: %w", err)
	}
	if g, _, ok := s.cache.Get(id); ok {
		return g[f], nil
	}
	var g domain.Grants
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		g, txErr = s.grantsTx(ctx, tx)
		return txErr
	})
	if err != nil {
		return domain.Grant{}, err
	}
	return g[f], nil
}

func checkKind(f domain.Feature, k domain.Kind) error {
	if f.Kind() != k {
		return fmt.Errorf("%w: feature %q is not a %s feature", domain.ErrInvalid, f, k)
	}
	return nil
}

// Allowed reports whether the store in ctx has the flag feature f.
func (s *Service) Allowed(ctx context.Context, f domain.Feature) (bool, error) {
	if err := checkKind(f, domain.Flag); err != nil {
		return false, err
	}
	g, err := s.grant(ctx, f)
	if err != nil {
		return false, err
	}
	return g.Unlimited || g.Value > 0, nil
}

// CheckCount reports whether the store in ctx may hold after units of
// the counted feature f: domain.ErrLimitReached when its plan grants
// fewer. The grant comes back either way, for the merchant's message.
func (s *Service) CheckCount(ctx context.Context, f domain.Feature, after int64) (domain.Grant, error) {
	if err := checkKind(f, domain.Count); err != nil {
		return domain.Grant{}, err
	}
	g, err := s.grant(ctx, f)
	if err != nil {
		return domain.Grant{}, err
	}
	if !g.Unlimited && after > g.Value {
		return g, fmt.Errorf("%w: %s %d of %d", domain.ErrLimitReached, f, after, g.Value)
	}
	return g, nil
}

func checkUnits(f domain.Feature, n int64) error {
	if !f.Valid() || n < 1 || n > domain.MaxUnits {
		return fmt.Errorf("%w: feature %q, %d units", domain.ErrInvalid, f, n)
	}
	return nil
}

// limitTx is the store-in-ctx's monthly limit of f, read in tx on a
// cache miss so a caller's transaction needs no second connection.
func (s *Service) limitTx(ctx context.Context, tx pgx.Tx, f domain.Feature) (int64, error) {
	g, err := s.grantsTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return g[f].Limit(), nil
}

// ConsumeTx counts n units of f for the store in ctx in this month, in
// tx, so they count only if tx commits. Past the limit it counts nothing
// and returns domain.ErrQuotaExceeded with the month's usage as it is.
// Concurrent consumers of a store wait on its row, so the last unit is
// spent once.
func (s *Service) ConsumeTx(ctx context.Context, tx pgx.Tx, f domain.Feature, n int64) (domain.Usage, error) {
	if err := checkUnits(f, n); err != nil {
		return domain.Usage{}, err
	}
	limit, err := s.limitTx(ctx, tx, f)
	if err != nil {
		return domain.Usage{}, err
	}
	u := domain.Usage{Feature: f, Period: domain.PeriodOf(s.now()), Limit: limit}
	if n <= limit {
		used, ok, cErr := store.Consume(ctx, tx, f, u.Period, n, limit)
		if cErr != nil {
			return domain.Usage{}, cErr
		}
		if ok {
			u.Used = used
			return u, nil
		}
	}
	if u.Used, err = store.Used(ctx, tx, f, u.Period); err != nil {
		return domain.Usage{}, err
	}
	return u, domain.ErrQuotaExceeded
}

// ReleaseTx gives back n units of f that ConsumeTx counted in period
// (the Usage it returned), in tx; never below zero.
func (s *Service) ReleaseTx(ctx context.Context, tx pgx.Tx, f domain.Feature, period time.Time, n int64) (domain.Usage, error) {
	if err := checkUnits(f, n); err != nil {
		return domain.Usage{}, err
	}
	if !domain.ValidPeriod(period) {
		return domain.Usage{}, fmt.Errorf("%w: period %v", domain.ErrInvalid, period)
	}
	limit, err := s.limitTx(ctx, tx, f)
	if err != nil {
		return domain.Usage{}, err
	}
	used, err := store.Release(ctx, tx, f, period, n)
	if err != nil {
		return domain.Usage{}, err
	}
	return domain.Usage{Feature: f, Period: period, Used: used, Limit: limit}, nil
}

// Usage is the store's usage of f in this month.
func (s *Service) Usage(ctx context.Context, f domain.Feature) (domain.Usage, error) {
	if !f.Valid() {
		return domain.Usage{}, fmt.Errorf("%w: feature %q", domain.ErrInvalid, f)
	}
	u := domain.Usage{Feature: f, Period: domain.PeriodOf(s.now())}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if u.Limit, txErr = s.limitTx(ctx, tx, f); txErr != nil {
			return txErr
		}
		u.Used, txErr = store.Used(ctx, tx, f, u.Period)
		return txErr
	})
	if err != nil {
		return domain.Usage{}, err
	}
	return u, nil
}
