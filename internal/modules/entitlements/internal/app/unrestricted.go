package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
)

// Unrestricted allows everything and counts nothing, without a database:
// what a module gets when it is built without entitlements (its own
// tests). cmd/platform always wires the real service.
type Unrestricted struct{}

// Allowed implements the interface: every flag is on.
func (Unrestricted) Allowed(context.Context, domain.Feature) (bool, error) { return true, nil }

// Require implements the interface: every flag is on.
func (Unrestricted) Require(context.Context, domain.Feature) error { return nil }

// AdmitTx implements the interface: no limit.
func (Unrestricted) AdmitTx(context.Context, pgx.Tx, domain.Feature, int64, func(context.Context, pgx.Tx) (int64, error)) (domain.Grant, error) {
	return domain.Grant{Unlimited: true}, nil
}

func unmetered(f domain.Feature, now time.Time) domain.Usage {
	return domain.Usage{Feature: f, Period: domain.PeriodOf(now), Limit: domain.Unlimited}
}

// CountTx implements the interface: nothing is counted.
func (Unrestricted) CountTx(_ context.Context, _ pgx.Tx, f domain.Feature, _ int64) (domain.Usage, error) {
	return unmetered(f, time.Now()), nil
}

// ConsumeTx implements the interface: nothing is counted.
func (Unrestricted) ConsumeTx(_ context.Context, _ pgx.Tx, f domain.Feature, _ int64) (domain.Usage, error) {
	return unmetered(f, time.Now()), nil
}

// ReleaseTx implements the interface.
func (Unrestricted) ReleaseTx(_ context.Context, _ pgx.Tx, f domain.Feature, period time.Time, _ int64) (domain.Usage, error) {
	return domain.Usage{Feature: f, Period: period, Limit: domain.Unlimited}, nil
}

// Usage implements the interface.
func (Unrestricted) Usage(_ context.Context, f domain.Feature) (domain.Usage, error) {
	return unmetered(f, time.Now()), nil
}
