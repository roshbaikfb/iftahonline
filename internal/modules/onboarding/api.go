// Package onboarding owns the new store's setup wizard (ADR-0034): the
// merchant's choice of activity, look and colour, and the starter
// template applied from it. This file is the module's only exported
// surface (ADR-0001); see docs/modules/onboarding.md.
package onboarding

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
)

// Starter is what sign-up (identity) uses.
type Starter interface {
	// StartTx opens the wizard of the store in ctx, in sign-up's tenant
	// transaction, so every store made from now on has one. A second
	// call is a no-op.
	StartTx(ctx context.Context, tx pgx.Tx) error
}

// Choice is the merchant's pick: what the store sells, its look and its
// main colour (#rrggbb).
type Choice = domain.Choice

// Activity is what the store sells.
type Activity = domain.Activity

// Look is the storefront's layout.
type Look = domain.Look

var _ Starter = (*app.Service)(nil)
