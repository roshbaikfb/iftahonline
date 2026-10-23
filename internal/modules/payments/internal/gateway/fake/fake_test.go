package fake_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/gatewaytest"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

const (
	key    = "merchant-key"
	secret = "merchant-webhook-secret"
)

func TestContract(t *testing.T) {
	t.Parallel()
	g := fake.New()
	gatewaytest.Run(t, gatewaytest.Harness{
		Gateway: g, Creds: gateway.Credentials{APIKey: key, WebhookSecret: secret}, BadCreds: gateway.Credentials{APIKey: fake.InvalidKey},
		Currency: money.EGP,
		Pay: func(t *testing.T, ref string) {
			t.Helper()
			if _, err := g.Pay(key, ref); err != nil {
				t.Fatal(err)
			}
		},
		Decline: func(t *testing.T, ref string) {
			t.Helper()
			if err := g.Decline(key, ref); err != nil {
				t.Fatal(err)
			}
		},
		Webhook: func(t *testing.T, ref string) gateway.WebhookRequest {
			t.Helper()
			body, sig, err := g.Webhook(key, secret, ref)
			if err != nil {
				t.Fatal(err)
			}
			h := http.Header{}
			h.Set(fake.SignatureHeader, sig)
			return gateway.WebhookRequest{Header: h, Body: body}
		},
	})
}

func TestCredentialsNeverPrint(t *testing.T) {
	t.Parallel()
	c := gateway.Credentials{SecretKey: "very-secret", APIKey: "api-secret", WebhookSecret: "hook-secret"}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v %+v %#v %s", c, c, c, c), c.LogValue().String()} {
		if strings.Contains(s, "secret") && !strings.Contains(s, "redacted") || strings.Contains(s, "very-secret") || strings.Contains(s, "api-secret") {
			t.Fatalf("leaked: %s", s)
		}
	}
}
