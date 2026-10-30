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
	// UsageCrossed is published when a monthly count crosses 80% or 100%
	// of the limit.
	UsageCrossed = domain.UsageCrossed
)

// EventUsageCrossed is UsageCrossed's type.
const EventUsageCrossed = domain.EventUsageCrossed

// PlanLimitCode is the problem code of a request the store's plan does
// not allow.
const PlanLimitCode = domain.PlanLimitCode

// Source is where grants come from (FromBilling, Fixed).
type Source = app.Source

// Monthly features: ConsumeTx, ReleaseTx, Usage.
const (
	// Messages are customer messages (WhatsApp or SMS), one unit each.
	Messages       = domain.Messages
	OrdersPerMonth = domain.OrdersPerMonth
)

// Counted features: AdmitTx.
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
	// AdmitTx admits n more of the counted feature f in the caller's
	// tenant transaction. count says how many the store holds; it runs
	// under a lock on (store, f) held until tx ends, so concurrent
	// creations cannot pass the limit together. Past the limit it is a
	// 403 with code plan_limit (errors.Is ErrLimitReached): return it.
	AdmitTx(ctx context.Context, tx pgx.Tx, f Feature, n int64, count func(context.Context, pgx.Tx) (int64, error)) (Grant, error)
	// CountTx counts n units of the monthly feature f in the caller's
	// tenant transaction and never refuses (a soft limit: orders). It
	// publishes UsageCrossed at 80% and 100%, once per month.
	CountTx(ctx context.Context, tx pgx.Tx, f Feature, n int64) (Usage, error)
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

var (
	_ Entitlements = (*app.Service)(nil)
	_ Entitlements = app.Unrestricted{}
)

// Unrestricted allows everything and counts nothing: the default of a
// module built without entitlements, in its own tests. cmd/platform
// always wires the real service.
func Unrestricted() Entitlements { return app.Unrestricted{} }
