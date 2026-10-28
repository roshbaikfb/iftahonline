// Package entitlements meters what each store may use per month
// (ADR-0038). Until plans (P7) every store gets the same limits from the
// environment; P7 changes where limits come from, not this interface.
// This file is the module's only exported surface (ADR-0001); see
// docs/modules/entitlements.md.
package entitlements

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
)

// DTOs.
type (
	// Feature is a metered capability.
	Feature = domain.Feature
	// Usage is a store's count for a feature in one month (UTC).
	Usage = domain.Usage
	// Threshold is a share of the limit a merchant is warned at.
	Threshold = domain.Threshold
)

// Metered features.
const (
	// Messages are customer messages (WhatsApp or SMS), one unit each.
	Messages = domain.Messages
)

// Warning thresholds (Usage.CrossedBy, Usage.Level).
const (
	NoThreshold = domain.NoThreshold
	Nearly      = domain.Nearly
	Reached     = domain.Reached
)

// Errors. Match with errors.Is.
var (
	// ErrQuotaExceeded: nothing was counted; the Usage returned with it
	// is the month's as it is.
	ErrQuotaExceeded = domain.ErrQuotaExceeded
	// ErrInvalid is a programming error: unknown feature, units outside
	// 1..1000, or a period that is not a month's first day.
	ErrInvalid = domain.ErrInvalid
)

// Entitlements is what metered modules use. Every method acts on the
// store in ctx.
type Entitlements interface {
	// ConsumeTx counts n units of f this month in the caller's tenant
	// transaction, so they count only if it commits. Past the limit it
	// counts nothing and returns ErrQuotaExceeded. Warn the merchant
	// when the returned Usage's CrossedBy(n) is not NoThreshold.
	ConsumeTx(ctx context.Context, tx pgx.Tx, f Feature, n int64) (Usage, error)
	// ReleaseTx gives back n units counted in period (Usage.Period of
	// the consumption), never below zero.
	ReleaseTx(ctx context.Context, tx pgx.Tx, f Feature, period time.Time, n int64) (Usage, error)
	// Usage is the store's usage of f this month.
	Usage(ctx context.Context, f Feature) (Usage, error)
}

var _ Entitlements = (*app.Service)(nil)
