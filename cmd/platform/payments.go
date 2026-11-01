package main

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/server"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

// newPayments builds the payments module (api and worker roles). inbox
// is the worker's, for webhook jobs; ten (the api role's) checks the
// card checkout's return URLs (ADR-0030), and messaging the links that
// pay a prepaid delivery (ADR-0070). Each is nil in the other role.
func newPayments(c clients, ords orders.Payments, inbox webhooks.Inbox, ten *tenancy.Module) (*payments.Module, error) {
	sealer, err := envelope.New(envelope.FromKeyring(c.cfg.Secrets.Keyring))
	if err != nil {
		return nil, err
	}
	ins, err := server.NewRiverInserter(c.pool, c.log)
	if err != nil {
		return nil, err
	}
	d := payments.Deps{
		Pool: c.pool, IDs: c.ids, Audit: identity.NewAuditRecorder(c.ids), Events: c.events, Gateways: c.cfg.Payments,
		Sealer: sealer, PublicURL: c.cfg.Mail.DashboardURL, Orders: ords, Jobs: ins, Inbox: inbox, Log: c.log, Plan: c.plan,
	}
	if ten != nil {
		if d.Limiter, err = ratelimit.New(c.rdb, "checkout"); err != nil {
			return nil, err
		}
		d.ClientIP = middleware.ClientIP
		resolver := ten.Resolver()
		d.Hosts = func(ctx context.Context, host string) (tenant.StoreID, error) {
			res, resErr := resolver.Resolve(ctx, host)
			return res.StoreID, resErr
		}
		links, linksErr := messaging.NewPayLinks(c.pool)
		if linksErr != nil {
			return nil, linksErr
		}
		d.DepositLinks = links.ValidPayLink
	}
	return payments.New(d)
}

// lateHandler is an event handler supplied after the bus subscriptions
// are made: every role subscribes (publishers need the subscriber list),
// and only the worker, which runs handlers, sets it before River starts.
type lateHandler struct {
	h atomic.Pointer[eventbus.Handler]
}

var errHandlerNotSet = errors.New("platform: event handler not set in this role")

// Set supplies the handler.
func (l *lateHandler) Set(h eventbus.Handler) { l.h.Store(&h) }

// Handle implements eventbus.Handler; before Set, it fails (the delivery
// is retried).
func (l *lateHandler) Handle(ctx context.Context, d eventbus.Delivery) error {
	h := l.h.Load()
	if h == nil {
		return errHandlerNotSet
	}
	return (*h)(ctx, d)
}
