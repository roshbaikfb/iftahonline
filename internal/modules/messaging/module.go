package messaging

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/twilio"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/whatsapp"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool    *pgxpool.Pool
	IDs     *ulid.Generator
	Secrets config.Secrets
	Config  config.Messaging
	Quota   entitlements.Entitlements
	Jobs    jobs.Inserter
	// PublicURL is the platform's public origin, for status callback URLs.
	PublicURL string
	// Inbox reads accepted webhooks (the worker role; nil in the api).
	Inbox webhooks.Inbox
	// Audit records merchant actions (identity's audit log); nil
	// discards.
	Audit audit.Recorder
	// The order confirmation's collaborators (the worker role; the api
	// role leaves them nil): orders to read and move, the store's name,
	// locale and host, and the staff emails and mailer for notices.
	Orders       orders.Fulfilment
	Stores       StoreSource
	Contacts     Contacts
	Mailer       mailer.Mailer
	DashboardURL string
	Log          *slog.Logger
}

// Module is the wired messaging module.
type Module struct {
	svc     *app.Service
	wa, sms *fake.Channel // the fake channels, when used
}

// derive makes a key for purpose from the platform's OTP pepper, so the
// module needs no secret of its own.
func derive(pepper []byte, purpose string) ([]byte, error) {
	return hkdf.Key(sha256.New, pepper, nil, "iftahonline messaging "+purpose, 32)
}

// New wires the module and its channels: the real accounts that are
// configured, else the fake ones when allowed.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Secrets.Keyring == nil || len(d.Secrets.OTPPepper) < 32 || d.Quota == nil || d.Jobs == nil ||
		d.PublicURL == "" || d.Log == nil {
		return nil, errors.New("messaging: missing dependency")
	}
	buttons, err := derive(d.Secrets.OTPPepper, "button payload v1")
	if err != nil {
		return nil, fmt.Errorf("messaging: %w", err)
	}
	chans, err := channels(d)
	if err != nil {
		return nil, err
	}
	m := &Module{svc: app.NewService(app.Deps{
		DB: d.Pool, IDs: d.IDs, Keyring: d.Secrets.Keyring, Quota: d.Quota, Jobs: d.Jobs, Channels: chans,
		Buttons: domain.ButtonKey(buttons), Inbox: d.Inbox, VerifyToken: d.Config.WhatsAppVerifyToken, Audit: d.Audit, Log: d.Log,
		Confirmations: app.Confirmations{Number: d.Config.WhatsAppDisplayNumber, Orders: d.Orders, Stores: d.Stores, Contacts: d.Contacts,
			Mailer: d.Mailer, DashboardURL: d.DashboardURL},
	})}
	if wa, ok := chans.WhatsApp.(*fake.Channel); ok {
		m.wa = wa
	}
	if sms, ok := chans.SMS.(*fake.Channel); ok {
		m.sms = sms
	}
	return m, nil
}

func channels(d Deps) (app.Channels, error) {
	c := d.Config
	var out app.Channels
	if c.WhatsApp() {
		wa, err := whatsapp.New(whatsapp.Options{GraphURL: c.WhatsAppGraphURL, PhoneNumberID: c.WhatsAppPhoneNumberID,
			AccessToken: c.WhatsAppAccessToken, AppSecret: c.WhatsAppAppSecret})
		if err != nil {
			return app.Channels{}, err
		}
		out.WhatsApp = wa
	}
	if c.SMS() {
		tw, err := twilio.New(twilio.Options{BaseURL: c.TwilioBaseURL, AccountSID: c.TwilioAccountSID, AuthToken: c.TwilioAuthToken,
			From: c.TwilioFrom, CallbackURL: webhooks.PlatformURL(d.PublicURL, twilio.Name)})
		if err != nil {
			return app.Channels{}, err
		}
		out.SMS = tw
	}
	if c.FakeChannels {
		secret, err := derive(d.Secrets.OTPPepper, "fake channels")
		if err != nil {
			return app.Channels{}, fmt.Errorf("messaging: %w", err)
		}
		if out.WhatsApp == nil {
			out.WhatsApp = fake.New("fake_whatsapp", secret)
		}
		if out.SMS == nil {
			out.SMS = fake.New("fake_sms", secret)
		}
	}
	return out, nil
}

// Mount registers the merchant API (the message log, the quota, the
// store's texts). scope is identity's membership middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// LinksDeps are the SMS link page's dependencies (the storefront role,
// which holds no secrets and sends nothing).
type LinksDeps struct {
	// Number is the platform's WhatsApp number (E.164) for the
	// thank-you page's link; "" offers none.
	Number string
	Pool   *pgxpool.Pool
	IDs    *ulid.Generator
	Jobs   jobs.Inserter
	Orders orders.Fulfilment
	Log    *slog.Logger
}

// NewConfirmLinks wires the SMS link page's side of the confirmation.
func NewConfirmLinks(d LinksDeps) (ConfirmLinks, error) {
	if d.Pool == nil || d.IDs == nil || d.Jobs == nil || d.Orders == nil || d.Log == nil {
		return nil, errors.New("messaging: missing confirm-link dependency")
	}
	return app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Jobs: d.Jobs, Log: d.Log, Confirmations: app.Confirmations{Orders: d.Orders, Number: d.Number}}), nil
}

// OrderPlacedHandler asks a new cash-on-delivery order's customer to
// confirm it. Subscribe it as ConfirmOrdersSubscriber to
// orders.EventOrderPlaced (worker role).
func (m *Module) OrderPlacedHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderPlaced) error { return m.svc.ConfirmOrder(ctx, ev.OrderID) })
}

// PlanLimitHandler warns the merchant when a monthly limit of the
// store's plan is near or reached. Subscribe it as PlanLimitsSubscriber
// to entitlements.EventUsageCrossed (worker role).
func (m *Module) PlanLimitHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev entitlements.UsageCrossed) error { return m.svc.PlanLimitCrossed(ctx, ev) })
}

// Fakes returns the fake WhatsApp and SMS channels when they are used
// (MESSAGING_FAKE_CHANNELS), for tests and demos; nil otherwise.
func (m *Module) Fakes() (whatsapp, sms *fake.Channel) { return m.wa, m.sms }

// Sender is what modules that message customers use.
func (m *Module) Sender() Sender { return m.svc }

// Merchant sends the platform's WhatsApp notices to stores' owners.
func (m *Module) Merchant() MerchantSender { return m.svc }

// PlatformProviders are the channels' webhook providers, for
// webhooks.Module.RegisterPlatform.
func (m *Module) PlatformProviders() []webhooks.PlatformProvider { return m.svc.PlatformProviders() }

// RegisterWorkers adds the module's job workers (the worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }
