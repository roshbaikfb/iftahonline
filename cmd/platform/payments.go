package main

import (
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/server"
)

// newPayments builds the payments module (api and worker roles). inbox
// is the worker's, for webhook jobs; nil in the api role.
func newPayments(c clients, ords orders.Payments, inbox webhooks.Inbox) (*payments.Module, error) {
	sealer, err := envelope.New(envelope.FromKeyring(c.cfg.Secrets.Keyring))
	if err != nil {
		return nil, err
	}
	ins, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	return payments.New(payments.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events, Gateways: c.cfg.Payments,
		Sealer: sealer, PublicURL: c.cfg.Mail.DashboardURL, Orders: ords, Jobs: ins, Inbox: inbox,
	})
}
