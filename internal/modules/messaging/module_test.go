package messaging_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func names(m *messaging.Module) []string {
	var out []string
	for _, p := range m.PlatformProviders() {
		out = append(out, p.Name())
	}
	return out
}

// TestChannelsFromConfig: real accounts when configured, the fakes only
// where allowed and nothing real is set.
func TestChannelsFromConfig(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none") // never connected
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	keyring, err := secretbox.ParseKeyring("1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	deps := func(c config.Messaging) messaging.Deps {
		return messaging.Deps{Pool: pool, IDs: ulid.NewGenerator(), Secrets: config.Secrets{Keyring: keyring, OTPPepper: make([]byte, 32)},
			Config: c, Quota: ent.Entitlements(), Jobs: testkit.NewJobs(), PublicURL: "https://app.example.com", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	realCfg := config.Messaging{WhatsAppPhoneNumberID: "1065", WhatsAppAccessToken: "t", WhatsAppAppSecret: "s", WhatsAppVerifyToken: "v",
		TwilioAccountSID: "AC1", TwilioAuthToken: "t", TwilioFrom: "+15005550006"}
	tests := []struct {
		name string
		cfg  config.Messaging
		want string
	}{
		{"none", config.Messaging{}, ""},
		{"fakes", config.Messaging{FakeChannels: true}, "fake_whatsapp,fake_sms"},
		{"real", realCfg, "whatsapp,twilio"},
		{"real wins over fakes", func() config.Messaging { c := realCfg; c.FakeChannels = true; return c }(), "whatsapp,twilio"},
		{"SMS only, fake WhatsApp", config.Messaging{TwilioAccountSID: "AC1", TwilioAuthToken: "t", TwilioFrom: "+1", FakeChannels: true}, "fake_whatsapp,twilio"},
	}
	for _, tt := range tests {
		m, nErr := messaging.New(deps(tt.cfg))
		if nErr != nil {
			t.Fatalf("%s: %v", tt.name, nErr)
		}
		got := ""
		for i, n := range names(m) {
			if i > 0 {
				got += ","
			}
			got += n
		}
		if got != tt.want {
			t.Errorf("%s: providers %q, want %q", tt.name, got, tt.want)
		}
		if m.Sender() == nil {
			t.Errorf("%s: no sender", tt.name)
		}
	}
	d := deps(config.Messaging{})
	d.PublicURL = ""
	if _, err = messaging.New(d); err == nil {
		t.Fatal("missing public URL accepted")
	}
	d = deps(config.Messaging{})
	d.Secrets.OTPPepper = nil
	if _, err = messaging.New(d); err == nil {
		t.Fatal("missing pepper accepted")
	}
}
