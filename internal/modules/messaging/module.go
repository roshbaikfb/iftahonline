package messaging

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/twilio"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/whatsapp"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
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
	Log   *slog.Logger
}

// Module is the wired messaging module.
type Module struct {
	svc *app.Service
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
	return &Module{svc: app.NewService(app.Deps{
		DB: d.Pool, IDs: d.IDs, Keyring: d.Secrets.Keyring, Quota: d.Quota, Jobs: d.Jobs, Channels: chans,
		Buttons: domain.ButtonKey(buttons), Inbox: d.Inbox, VerifyToken: d.Config.WhatsAppVerifyToken, Log: d.Log,
	})}, nil
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

// Sender is what modules that message customers use.
func (m *Module) Sender() Sender { return m.svc }

// SetReplyHandler supplies what acts on customers' answers (at startup,
// before jobs run).
func (m *Module) SetReplyHandler(h ReplyHandler) { m.svc.SetReplyHandler(h) }

// PlatformProviders are the channels' webhook providers, for
// webhooks.Module.RegisterPlatform.
func (m *Module) PlatformProviders() []webhooks.PlatformProvider { return m.svc.PlatformProviders() }

// RegisterWorkers adds the module's job workers (the worker role).
func (m *Module) RegisterWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }
