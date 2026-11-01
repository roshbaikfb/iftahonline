// Package risk scores customers' phones for merchants (ADR-0065): every
// store's delivered, returned, refused and unanswered orders teach the
// platform about a phone, most recent first, and merchants see only a
// band (new, low, medium, high) beside their own store's history of the
// phone. No store ever reads another store's orders. This file is the
// module's only exported surface (ADR-0001); see docs/modules/risk.md.
package risk

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
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

// LearnSubscriber is the event bus subscriber name of
// Module.LearnHandler, for both orders.EventOrderPlaced and
// orders.EventOrderStatusChanged.
const LearnSubscriber = "risk.learn"
