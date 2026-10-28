// Package app holds the quota use cases (ADR-0038): consume units of a
// feature in the caller's transaction, give them back, and read a
// store's usage.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Limits says how many units of a feature the store in ctx may use per
// month. Until plans (P7) every store gets the same; P7 implements it
// from the store's plan.
type Limits interface {
	Limit(ctx context.Context, f domain.Feature) (int64, error)
}

// StaticLimits gives every store the same limits; a feature it does not
// list has none (0).
type StaticLimits map[domain.Feature]int64

// Limit implements Limits.
func (l StaticLimits) Limit(_ context.Context, f domain.Feature) (int64, error) { return l[f], nil }

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	limits Limits
	now    func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	Limits Limits
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: d.DB, limits: d.Limits, now: now}
}

func checkUnits(f domain.Feature, n int64) error {
	if !f.Valid() || n < 1 || n > domain.MaxUnits {
		return fmt.Errorf("%w: feature %q, %d units", domain.ErrInvalid, f, n)
	}
	return nil
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
	limit, err := s.limits.Limit(ctx, f)
	if err != nil {
		return domain.Usage{}, fmt.Errorf("entitlements: limit: %w", err)
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
	limit, err := s.limits.Limit(ctx, f)
	if err != nil {
		return domain.Usage{}, fmt.Errorf("entitlements: limit: %w", err)
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
	limit, err := s.limits.Limit(ctx, f)
	if err != nil {
		return domain.Usage{}, fmt.Errorf("entitlements: limit: %w", err)
	}
	u := domain.Usage{Feature: f, Period: domain.PeriodOf(s.now()), Limit: limit}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		u.Used, txErr = store.Used(ctx, tx, f, u.Period)
		return txErr
	})
	if err != nil {
		return domain.Usage{}, err
	}
	return u, nil
}
