// Package billing owns plans, what they grant, and each store's
// subscription (ADR-0045). It is the only place that knows plans by
// name: other modules ask the entitlements module what a store may do,
// never which plan it is on (internal/tools/noplannames).
// This file is the module's only exported surface (ADR-0001); see
// docs/modules/billing.md.
package billing

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
)

// DTOs.
type (
	// Grant is what a plan gives of one feature. Unlimited, or a Value:
	// 0 or 1 for a flag, a count, or units per calendar month.
	Grant = domain.Grant
	// Grants are by feature key; a feature not listed is not granted.
	Grants = domain.Grants
)

// GrantSource is what the entitlements module reads.
type GrantSource interface {
	// GrantsTx are what the store in ctx is granted now, read in its
	// tenant transaction tx: its plan's while the plan applies (trial
	// running, paid, or past due in grace), the free plan's otherwise.
	GrantsTx(ctx context.Context, tx pgx.Tx) (Grants, error)
}

var _ GrantSource = (*app.Service)(nil)
