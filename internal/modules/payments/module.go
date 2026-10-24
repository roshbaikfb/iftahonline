package payments

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/paymob"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/payapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	IDs  *ulid.Generator
	// Audit records changes (identity's audit log); nil discards.
	Audit audit.Recorder
	// Events receives payments.payment.status_changed; nil discards.
	Events eventbus.Publisher
	// Gateways configures the adapters merchants may connect.
	Gateways config.Payments
	// Sealer seals gateway keys.
	Sealer *envelope.Sealer
	// PublicURL is the platform's public origin, for webhook URLs.
	PublicURL string
	// Orders reads, marks and moves the orders payments are for.
	Orders orders.Payments
	// Jobs enqueues payment jobs in the caller's transaction.
	Jobs jobs.Inserter
	// Inbox gives webhook jobs their receipts (worker).
	Inbox webhooks.Inbox

	// The shopper's checkout routes (api role, MountCheckout):
	// Hosts checks return URLs, Limiter and ClientIP bound checkouts per
	// address.
	Hosts    HostOwner
	Limiter  *ratelimit.Limiter
	ClientIP ClientIPFunc
	Log      *slog.Logger
}

// gateways builds the adapters c offers, and the fake one, whose hosted
// pages are served under publicURL.
func gateways(c config.Payments, publicURL string) (app.Gateways, *fake.Gateway) {
	out := app.Gateways{paymob.Name: paymob.New(paymob.Options{BaseURL: c.PaymobBaseURL})}
	if !c.FakeGateway {
		return out, nil
	}
	f := fake.NewWithCheckout(strings.TrimRight(publicURL, "/") + payapi.FakeCheckoutPath)
	out[fake.Name] = f
	return out, f
}

// payer adapts the fake gateway to FakePayer.
type payer struct{ f *fake.Gateway }

func (p payer) Pay(apiKey string, id ulid.ID) error {
	_, err := p.f.Pay(apiKey, id.String())
	return err
}

func (p payer) Webhook(apiKey, secret string, id ulid.ID) ([]byte, http.Header, error) {
	body, sig, err := p.f.Webhook(apiKey, secret, id.String())
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	h.Set(fake.SignatureHeader, sig)
	return body, h, nil
}

// Module is the wired payments module.
type Module struct {
	svc  *app.Service
	fake *fake.Gateway
	d    Deps
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Sealer == nil || d.Orders == nil || d.Jobs == nil || d.PublicURL == "" {
		return nil, errors.New("payments: missing dependency")
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	gs, f := gateways(d.Gateways, d.PublicURL)
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events, Gateways: gs,
		Sealer: d.Sealer, PublicURL: d.PublicURL, Orders: d.Orders, Jobs: d.Jobs, Inbox: d.Inbox}), fake: f, d: d}, nil
}

// Checkout is what the storefront uses to take card payments.
func (m *Module) Checkout() Checkout { return m.svc }

// FakePayer pays the fake gateway's payments; nil unless the fake
// gateway is enabled (never in production).
func (m *Module) FakePayer() FakePayer {
	if m.fake == nil {
		return nil
	}
	return payer{f: m.fake}
}

// RegisterWorkers adds the payment jobs' workers (worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// WebhookProviders are the gateways' sides of the webhooks inbox, one per
// gateway; register them on the webhooks module.
func (m *Module) WebhookProviders() []webhooks.Provider { return m.svc.WebhookProviders() }

// MountCheckout registers the shopper's checkout routes (public, api
// role), and the fake gateway's hosted page when it is enabled. It needs
// Deps.Hosts and ClientIP.
func (m *Module) MountCheckout(mux *http.ServeMux, resp *problem.Responder) error {
	if m.d.Hosts == nil || m.d.ClientIP == nil {
		return errors.New("payments: checkout needs Hosts and ClientIP")
	}
	payapi.Mount(mux, payapi.Deps{Service: m.svc, Hosts: m.d.Hosts, Limiter: m.d.Limiter, ClientIP: m.d.ClientIP, Problems: resp, Log: m.d.Log})
	if m.fake != nil {
		payapi.MountFakeGateway(mux, m.fake)
	}
	return nil
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}
