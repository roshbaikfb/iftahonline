package main

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/server"
)

// newMarketing builds the ad pixels module (ADR-0043): the api serves
// the merchant's pixels and consent mode; the storefront renders them
// and enqueues the cart's and checkout's conversions; the worker sends
// every conversion and reports orders' purchases (ords, nil elsewhere).
func newMarketing(c clients, ords orders.Conversions) (*marketing.Module, error) {
	var sealer *envelope.Sealer
	if c.cfg.Secrets.Keyring != nil { // the storefront role holds no keys
		var err error
		if sealer, err = envelope.New(envelope.FromKeyring(c.cfg.Secrets.Keyring)); err != nil {
			return nil, err
		}
	}
	ins, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return marketing.New(marketing.Deps{Pool: c.pool, IDs: c.ids, Sealer: sealer, Events: c.events, Audit: identity.NewAuditRecorder(c.ids),
		Jobs: ins, Orders: ords, Plan: c.plan})
}
