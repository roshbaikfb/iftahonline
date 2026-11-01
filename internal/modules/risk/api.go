// Package risk scores customers' phones for merchants (ADR-0065): every
// store's delivered, returned, refused and unanswered orders teach the
// platform about a phone, most recent first, and merchants see only a
// band (new, low, medium, high) beside their own store's history of the
// phone. No store ever reads another store's orders. This file is the
// module's only exported surface (ADR-0001); see docs/modules/risk.md.
package risk

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// Band is what merchants see of a phone's score.
type Band = domain.Band

// Bands.
const (
	BandNew    = domain.BandNew
	BandLow    = domain.BandLow
	BandMedium = domain.BandMedium
	BandHigh   = domain.BandHigh
)

// Action is what a store's rules do to a new storefront cash order
// (ADR-0066).
type Action = domain.Action

// Actions.
const (
	NoAction = domain.NoAction
	Hold     = domain.Hold
	Verify   = domain.Verify
)

// Verdict is the rules' answer for a new order: the band and the action.
type Verdict = app.Verdict

// Checker scores a new storefront cash order's phone and applies the
// store's rules, in the store in ctx; orders asks it before placing one
// (cmd/platform adapts it to orders.RiskCheck).
type Checker interface {
	Check(ctx context.Context, p phone.E164) (Verdict, error)
}

var _ Checker = (*app.Service)(nil)

// LearnSubscriber is the event bus subscriber name of
// Module.LearnHandler, for both orders.EventOrderPlaced and
// orders.EventOrderStatusChanged.
const LearnSubscriber = "risk.learn"
