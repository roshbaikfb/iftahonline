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

// TestHostedPage: the development page finds a payment by reference,
// pays or declines it once, and sends the shopper back.
func TestHostedPage(t *testing.T) {
	t.Parallel()
	g := fake.NewWithCheckout("http://localhost:18080/fake-gateway/checkout/")
	amount, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	creds := gateway.Credentials{APIKey: key}
	for _, ref := range []string{"pay-me", "decline-me"} {
		c, createErr := g.CreatePayment(t.Context(), creds, gateway.PaymentRequest{Reference: ref, Amount: amount,
			NotificationURL: "https://app.example.com/hook", RedirectURL: "https://app.example.com/back/" + ref})
		if createErr != nil || c.URL != "http://localhost:18080/fake-gateway/checkout/"+ref {
			t.Fatalf("checkout %+v %v", c, createErr)
		}
	}
	page, ok := g.PageOf("pay-me")
	if !ok || !page.Pending || page.Amount.Amount() != 52000 || page.Redirect != "https://app.example.com/back/pay-me" {
		t.Fatalf("page %+v %v", page, ok)
	}
	if err = g.Answer("pay-me", true); err != nil {
		t.Fatal(err)
	}
	if err = g.Answer("decline-me", false); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]gateway.State{"pay-me": gateway.StatePaid, "decline-me": gateway.StateFailed} {
		st, stErr := g.FetchStatus(t.Context(), creds, ref, money.EGP)
		if stErr != nil || st.State != want {
			t.Errorf("%s: %+v %v, want %s", ref, st, stErr, want)
		}
	}
	if err = g.Answer("pay-me", true); err == nil {
		t.Error("a paid payment was answered again")
	}
	if _, ok = g.PageOf("unknown"); ok {
		t.Error("an unknown payment has a page")
	}
	if err = g.Answer("unknown", true); err == nil {
		t.Error("an unknown payment was answered")
	}
}
