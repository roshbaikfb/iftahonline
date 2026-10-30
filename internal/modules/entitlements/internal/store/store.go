// Package store adapts the sqlc queries in entitlementsdb to domain
// types. Every function runs on a transaction opened by
// postgres.InTenantTx and takes the store ID from tenant.FromContext,
// never from its arguments (CLAUDE.md rule 5); RLS checks it a second
// time.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/store/entitlementsdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = entitlementsdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("entitlements store: %w", err)
	}
	return id.ID, nil
}

// Consume adds n to the month's count unless that passes limit. ok is
// false when it would; nothing is counted then. The caller ensures
// 0 < n <= limit.
func Consume(ctx context.Context, tx Tx, f domain.Feature, period time.Time, n, limit int64) (used int64, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return 0, false, err
	}
	used, err = entitlementsdb.New(tx).ConsumeUsage(ctx, entitlementsdb.ConsumeUsageParams{
		StoreID: store, Feature: string(f), Period: period, N: n, MaxUsed: limit,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("entitlements store: consume: %w", err)
	}
	return used, true, nil
}

// Release gives n back to the month, never below zero, and returns the
// new count (0 when the month counted nothing).
func Release(ctx context.Context, tx Tx, f domain.Feature, period time.Time, n int64) (int64, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	used, err := entitlementsdb.New(tx).ReleaseUsage(ctx, entitlementsdb.ReleaseUsageParams{
		StoreID: store, Feature: string(f), Period: period, N: n,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("entitlements store: release: %w", err)
	}
	return used, nil
}

// Used is the month's count (0 when nothing was counted).
func Used(ctx context.Context, tx Tx, f domain.Feature, period time.Time) (int64, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	used, err := entitlementsdb.New(tx).GetUsage(ctx, entitlementsdb.GetUsageParams{StoreID: store, Feature: string(f), Period: period})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("entitlements store: get: %w", err)
	}
	return used, nil
}

// Lock serializes the store's creations of the counted feature f until
// tx ends.
func Lock(ctx context.Context, tx Tx, f domain.Feature) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = entitlementsdb.New(tx).LockFeature(ctx, entitlementsdb.LockFeatureParams{StoreID: store, Feature: string(f)}); err != nil {
		return fmt.Errorf("entitlements store: lock: %w", err)
	}
	return nil
}
