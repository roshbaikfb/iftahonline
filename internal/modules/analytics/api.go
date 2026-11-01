// Package analytics is the net profit dashboard (ADR-0069): what the
// store's delivered orders earned after product costs, delivery, COD
// fees, returns and ad spend, by product, campaign and governorate. It
// keeps its own facts from the orders module's events, computes daily
// figures in scheduled jobs, and reads them on the read pool (ADR-0064).
// This file is the module's only exported surface (ADR-0001); see
// docs/modules/analytics.md.
package analytics

import "github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"

type (
	// StoreInfo is what analytics needs of a store: its currency and
	// time zone.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
	// AdSpendSource is an ad platform's spend reporting, for the
	// importers to come (Meta, TikTok, Google).
	AdSpendSource = app.AdSpendSource
	// AdSpendLine is one day's spend of one campaign.
	AdSpendLine = app.AdSpendLine
)

// LearnSubscriber is the event bus subscriber name of
// Module.LearnHandler, for orders.EventOrderPlaced,
// orders.EventOrderStatusChanged and orders.EventOrderPaymentChanged.
const LearnSubscriber = "analytics.learn"
