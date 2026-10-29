package main

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
)

// newMarketing builds the ad pixels module (ADR-0043): the api serves
// the merchant's pixels and consent mode.
func newMarketing(c clients) (*marketing.Module, error) {
	sealer, err := envelope.New(envelope.FromKeyring(c.cfg.Secrets.Keyring))
	if err != nil {
		return nil, err
	}
	return marketing.New(marketing.Deps{Pool: c.pool, IDs: c.ids, Sealer: sealer, Events: c.events, Audit: identity.NewAuditRecorder(c.ids)})
}
