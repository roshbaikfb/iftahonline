// Package entitlements is the one place that answers what a store may
// do (ADR-0038, ADR-0045): whether it has a feature, how many of a thing
// it may hold, and its monthly quotas. Grants come from the store's plan
// (the billing module); no other module looks at plans.
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
	// Grant is what a store is granted of one feature.
	Grant = domain.Grant
	// Grants are by feature; a feature not listed is not granted.
	Grants = domain.Grants
	// Fixed grants every store the same (tests and demo tools).
	Fixed = app.Fixed
)

// Source is where grants come from (FromBilling, Fixed).
type Source = app.Source

// Monthly features: ConsumeTx, ReleaseTx, Usage.
const (
	// Messages are customer messages (WhatsApp or SMS), one unit each.
	Messages       = domain.Messages
	OrdersPerMonth = domain.OrdersPerMonth
)

// Counted features: CheckCount.
const (
	Products      = domain.Products
	Staff         = domain.Staff
	CustomDomains = domain.CustomDomains
)

// Flag features: Allowed.
const (
	CardPayments = domain.CardPayments
	Coupons      = domain.Coupons
	Pixels       = domain.Pixels
	Feeds        = domain.Feeds
	// AdFree stores show no ads (the free plan does).
	AdFree = domain.AdFree
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
	// ErrLimitReached: the store holds what its plan grants of a counted
	// feature.
	ErrLimitReached = domain.ErrLimitReached
	// ErrInvalid is a programming error: a feature unknown or of the
	// wrong kind for the call, units outside 1..1000, or a period that is
	// not a month's first day.
	ErrInvalid = domain.ErrInvalid
)

// Entitlements is what other modules check and meter with. Every method
// acts on the store in ctx. Grants are cached per instance for at most
// 30 s; a cached check costs no query.
type Entitlements interface {
	// Allowed reports whether the store has the flag feature f.
	Allowed(ctx context.Context, f Feature) (bool, error)
	// CheckCount reports whether the store may hold after units of the
	// counted feature f (ErrLimitReached when not), with its grant.
	CheckCount(ctx context.Context, f Feature, after int64) (Grant, error)
	// ConsumeTx counts n units of the monthly feature f in the caller's
	// tenant transaction, so they count only if it commits. Past the limit it
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
