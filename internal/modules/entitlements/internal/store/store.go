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
	"github.com/jackc/pgx/v5/pgtype"

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

// Overrides are the store-in-ctx's per-store grants, expired ones
// included, by feature.
func Overrides(ctx context.Context, tx Tx) ([]domain.Override, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := entitlementsdb.New(tx).ListOverrides(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("entitlements store: overrides: %w", err)
	}
	out := make([]domain.Override, len(rows))
	for i, r := range rows {
		o := domain.Override{Feature: domain.Feature(r.Feature), Reason: r.Reason, UpdatedAt: r.UpdatedAt.Time}
		if r.Value.Valid {
			o.Grant.Value = r.Value.Int64
		} else {
			o.Grant.Unlimited = true
		}
		if r.ExpiresAt.Valid {
			o.ExpiresAt = r.ExpiresAt.Time
		}
		out[i] = o
	}
	return out, nil
}

// SaveOverride sets the store-in-ctx's grant of o.Feature.
func SaveOverride(ctx context.Context, tx Tx, o domain.Override) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	p := entitlementsdb.UpsertOverrideParams{StoreID: store, Feature: string(o.Feature), Reason: o.Reason,
		Value: pgtype.Int8{Int64: o.Grant.Value, Valid: !o.Grant.Unlimited}, ExpiresAt: pgtype.Timestamptz{Time: o.ExpiresAt, Valid: !o.ExpiresAt.IsZero()}}
	if err = entitlementsdb.New(tx).UpsertOverride(ctx, p); err != nil {
		return fmt.Errorf("entitlements store: save override: %w", err)
	}
	return nil
}

// DeleteOverride clears the store-in-ctx's grant of f; it reports
// whether there was one.
func DeleteOverride(ctx context.Context, tx Tx, f domain.Feature) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := entitlementsdb.New(tx).DeleteOverride(ctx, entitlementsdb.DeleteOverrideParams{StoreID: store, Feature: string(f)})
	if err != nil {
		return false, fmt.Errorf("entitlements store: delete override: %w", err)
	}
	return n > 0, nil
}
