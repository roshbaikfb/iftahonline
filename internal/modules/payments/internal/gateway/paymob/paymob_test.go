package paymob_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/gatewaytest"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/paymob"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/paymob/paymobtest"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

var fast = retry.Policy{MaxAttempts: 3, Initial: time.Millisecond, Max: 2 * time.Millisecond}

func standIn(t *testing.T) (*paymob.Gateway, *paymobtest.Server) {
	t.Helper()
	srv := paymobtest.New(t)
	return paymob.New(paymob.Options{Client: srv.Client(), BaseURL: srv.URL, Retry: &fast}), srv
}

func TestContract(t *testing.T) {
	t.Parallel()
	g, srv := standIn(t)
	bad := paymobtest.Credentials()
	bad.APIKey, bad.SecretKey = "wrong", "wrong"
	gatewaytest.Run(t, gatewaytest.Harness{
		Gateway: g, Creds: paymobtest.Credentials(), BadCreds: bad, Currency: money.EGP,
		Pay: func(t *testing.T, ref string) {
			t.Helper()
			if err := srv.Pay(ref); err != nil {
				t.Fatal(err)
			}
		},
		Decline: func(t *testing.T, ref string) {
			t.Helper()
			if err := srv.Decline(ref); err != nil {
				t.Fatal(err)
			}
		},
		Webhook: func(t *testing.T, ref string) gateway.WebhookRequest {
			t.Helper()
			body, q, err := srv.Callback(ref)
			if err != nil {
				t.Fatal(err)
			}
			return gateway.WebhookRequest{Query: q, Body: body}
		},
	})
}

// TestSandboxContract runs the suite against Paymob's test mode with the
// merchant's test keys (PAYMOB_SANDBOX_SECRET_KEY, _PUBLIC_KEY, _API_KEY,
// _HMAC_SECRET, _INTEGRATION_ID): the check that the shapes are Paymob's.
// Paying needs a person on the hosted page, so those cases are left out.
func TestSandboxContract(t *testing.T) {
	sk := os.Getenv("PAYMOB_SANDBOX_SECRET_KEY")
	if sk == "" {
		t.Skip("PAYMOB_SANDBOX_* not set: Paymob's sandbox contract is not run")
	}
	integration, err := strconv.Atoi(os.Getenv("PAYMOB_SANDBOX_INTEGRATION_ID"))
	if err != nil {
		t.Fatal("PAYMOB_SANDBOX_INTEGRATION_ID must be a number")
	}
	creds := gateway.Credentials{SecretKey: sk, PublicKey: os.Getenv("PAYMOB_SANDBOX_PUBLIC_KEY"), APIKey: os.Getenv("PAYMOB_SANDBOX_API_KEY"),
		WebhookSecret: os.Getenv("PAYMOB_SANDBOX_HMAC_SECRET"), IntegrationIDs: []int{integration}}
	bad := creds
	bad.APIKey, bad.SecretKey = "invalid", "invalid"
	gatewaytest.Run(t, gatewaytest.Harness{Gateway: paymob.New(paymob.Options{}), Creds: creds, BadCreds: bad, Currency: money.EGP})
}

// TestIntentionBody: what Paymob receives for a payment.
func TestIntentionBody(t *testing.T) {
	t.Parallel()
	g, srv := standIn(t)
	req := gatewaytest.Request(t, money.EGP)
	c, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Path != "/unifiedcheckout/" || u.Query().Get("publicKey") != paymobtest.PublicKey || u.Query().Get("clientSecret") != "csk_"+req.Reference ||
		!strings.HasPrefix(c.ProviderRef, "pi_test_") {
		t.Fatalf("checkout %+v", c)
	}
	in := srv.Intention(req.Reference)
	billing := in["billing_data"].(map[string]any)
	if in["amount"] != 52000.0 || in["currency"] != "EGP" || in["special_reference"] != req.Reference || in["notification_url"] != req.NotificationURL ||
		billing["first_name"] != "منى" || billing["phone_number"] != "+201012345678" || billing["email"] != "NA" {
		t.Fatalf("intention %v", in)
	}
	// A reused reference is refused, and not retried.
	if _, err = g.CreatePayment(t.Context(), paymobtest.Credentials(), req); !errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("reused reference: %v", err)
	}
	sar, err := money.New(100, money.SAR)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = paymob.New(paymob.Options{}).Refund(t.Context(), paymobtest.Credentials(), "abc", sar); !errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("refund of a non-numeric transaction: %v", err)
	}
	missing := paymobtest.Credentials()
	missing.IntegrationIDs = nil
	if err = g.Verify(t.Context(), missing); !errors.Is(err, gateway.ErrAuth) {
		t.Fatalf("no integration: %v", err)
	}
}

// TestCallbackHMAC: the signature covers Paymob's fields, not the body's
// bytes, so reordering or spacing the JSON keeps it valid, and changing
// any signed value breaks it.
func TestCallbackHMAC(t *testing.T) {
	t.Parallel()
	g, srv := standIn(t)
	req := gatewaytest.Request(t, money.EGP)
	if _, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req); err != nil {
		t.Fatal(err)
	}
	if err := srv.Pay(req.Reference); err != nil {
		t.Fatal(err)
	}
	body, q, err := srv.Callback(req.Reference)
	if err != nil {
		t.Fatal(err)
	}
	creds := paymobtest.Credentials()
	ev, err := g.VerifyWebhook(creds, gateway.WebhookRequest{Query: q, Body: body})
	if err != nil || ev.Reference != req.Reference || !strings.HasPrefix(ev.EventID, "txn:") {
		t.Fatalf("event %+v %v", ev, err)
	}
	spaced := []byte(strings.ReplaceAll(string(body), ",", ", "))
	if _, err = g.VerifyWebhook(creds, gateway.WebhookRequest{Query: q, Body: spaced}); err != nil {
		t.Fatalf("spaced JSON: %v", err)
	}
	upper := url.Values{"hmac": {strings.ToUpper(q.Get("hmac"))}}
	if _, err = g.VerifyWebhook(creds, gateway.WebhookRequest{Query: upper, Body: body}); err != nil {
		t.Fatalf("upper-case hex: %v", err)
	}
	for name, tamper := range map[string][2]string{
		"success":  {`"success":true`, `"success":false`},
		"order id": {`"merchant_order_id":"` + req.Reference + `"`, `"merchant_order_id":"other"`},
		"pending":  {`"pending":false`, `"pending":true`},
	} {
		forged := []byte(strings.Replace(string(body), tamper[0], tamper[1], 1))
		_, verr := g.VerifyWebhook(creds, gateway.WebhookRequest{Query: q, Body: forged})
		if name == "order id" {
			// merchant_order_id is not signed (only order.id is): the
			// reference is a hint the job re-queries, never trusted.
			if verr != nil {
				t.Errorf("%s: %v", name, verr)
			}
			continue
		}
		if !errors.Is(verr, gateway.ErrForged) {
			t.Errorf("%s changed: %v, want ErrForged", name, verr)
		}
	}
	for _, hm := range []string{"", "zz", strings.Repeat("a", 64)} {
		if _, err = g.VerifyWebhook(creds, gateway.WebhookRequest{Query: url.Values{"hmac": {hm}}, Body: body}); !errors.Is(err, gateway.ErrForged) {
			t.Errorf("hmac %q: %v", hm, err)
		}
	}
	if _, err = g.VerifyWebhook(creds, gateway.WebhookRequest{Query: q, Body: []byte(`[`)}); !errors.Is(err, gateway.ErrForged) {
		t.Errorf("garbage: %v", err)
	}
}

func FuzzVerifyWebhook(f *testing.F) {
	srv := paymobtest.New(f)
	g := paymob.New(paymob.Options{Client: srv.Client(), BaseURL: srv.URL, Retry: &fast})
	creds := paymobtest.Credentials()
	f.Add([]byte(`{"type":"TRANSACTION","obj":{"id":1,"order":{"id":2,"merchant_order_id":"r"}}}`), "00")
	f.Add([]byte(`{"obj":null}`), "")
	f.Add([]byte(`{"obj":{"source_data":"x","order":7}}`), strings.Repeat("ab", 64))
	f.Fuzz(func(t *testing.T, body []byte, hm string) {
		ev, err := g.VerifyWebhook(creds, gateway.WebhookRequest{Query: url.Values{"hmac": {hm}}, Body: body})
		if err == nil {
			want, signErr := paymob.Sign(paymobtest.HMACSecret, body)
			if signErr != nil || !strings.EqualFold(want, hm) || ev.Reference == "" || ev.EventID == "" {
				t.Fatalf("accepted %q with hmac %q: %+v", body, hm, ev)
			}
		}
	})
}

// TestRefundIsNeverRepeated: Paymob takes no idempotency key, so a refund
// whose answer is lost must not be sent again by the adapter: the money
// went back once, and the caller (the refund job) finds that out from
// the gateway's refunded amount before trying again.
func TestRefundIsNeverRepeated(t *testing.T) {
	t.Parallel()
	g, srv := standIn(t)
	req := gatewaytest.Request(t, money.EGP)
	if _, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req); err != nil {
		t.Fatal(err)
	}
	if err := srv.Pay(req.Reference); err != nil {
		t.Fatal(err)
	}
	st, err := g.FetchStatus(t.Context(), paymobtest.Credentials(), req.Reference, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := money.New(10000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	srv.LoseRefundReplies(1)
	if _, err = g.Refund(t.Context(), paymobtest.Credentials(), st.TransactionID, amount); err == nil || errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("a lost answer must be a retryable error, got %v", err)
	}
	if got := srv.Refunded(req.Reference); got != 10000 {
		t.Fatalf("Paymob refunded %d, want 10000 once", got)
	}
}

// TestStatusIsNotTakenOnTrust: an inquiry's answer counts as paid only
// when it is the transaction of the reference asked about, in the
// payment's currency, and captured (an authorization alone is not
// money taken).
func TestStatusIsNotTakenOnTrust(t *testing.T) {
	t.Parallel()
	for name, fields := range map[string]map[string]any{
		"another order's transaction": {"merchant_order_id": "someone-else"},
		"another currency":            {"currency": "USD"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g, srv := standIn(t)
			req := gatewaytest.Request(t, money.EGP)
			if _, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req); err != nil {
				t.Fatal(err)
			}
			if err := srv.Pay(req.Reference); err != nil {
				t.Fatal(err)
			}
			if err := srv.Override(req.Reference, fields); err != nil {
				t.Fatal(err)
			}
			st, err := g.FetchStatus(t.Context(), paymobtest.Credentials(), req.Reference, money.EGP)
			if err == nil || st.State == gateway.StatePaid {
				t.Fatalf("believed %+v (%v)", st, err)
			}
		})
	}
	t.Run("an authorization not captured", func(t *testing.T) {
		t.Parallel()
		g, srv := standIn(t)
		req := gatewaytest.Request(t, money.EGP)
		if _, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req); err != nil {
			t.Fatal(err)
		}
		if err := srv.Pay(req.Reference); err != nil {
			t.Fatal(err)
		}
		if err := srv.Override(req.Reference, map[string]any{"is_auth": true, "is_capture": false, "is_standalone_payment": false}); err != nil {
			t.Fatal(err)
		}
		st, err := g.FetchStatus(t.Context(), paymobtest.Credentials(), req.Reference, money.EGP)
		if err != nil || st.State != gateway.StatePending {
			t.Fatalf("an authorization: %+v %v, want pending", st, err)
		}
	})
}

// TestRefusalsCarryNoPersonalData: Paymob's error bodies may echo the
// customer's phone or email; what the adapter keeps of them (in errors,
// logs and the payment's failure) hides both.
func TestRefusalsCarryNoPersonalData(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"billing_data":{"phone_number":["+201012345678 is not valid"],"email":["mona.ahmed@example.com is taken"]}}`))
	}))
	t.Cleanup(srv.Close)
	g := paymob.New(paymob.Options{Client: srv.Client(), BaseURL: srv.URL, Retry: &fast})
	_, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), gatewaytest.Request(t, money.EGP))
	if !errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("want a refusal, got %v", err)
	}
	for _, leak := range []string{"1012345678", "mona.ahmed@example.com"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal keeps %q: %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("the refusal lost Paymob's reason: %v", err)
	}
}

// TestAuthTokenIsReused: an inquiry needs an auth token from the API
// key; one token serves a merchant's inquiries until Paymob stops taking
// it, then a new one is asked for, once.
func TestAuthTokenIsReused(t *testing.T) {
	t.Parallel()
	g, srv := standIn(t)
	req := gatewaytest.Request(t, money.EGP)
	if _, err := g.CreatePayment(t.Context(), paymobtest.Credentials(), req); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := g.FetchStatus(t.Context(), paymobtest.Credentials(), req.Reference, money.EGP); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.TokenCalls(); n != 1 {
		t.Fatalf("%d tokens asked for 3 inquiries, want 1", n)
	}
	srv.ExpireTokens()
	if _, err := g.FetchStatus(t.Context(), paymobtest.Credentials(), req.Reference, money.EGP); err != nil {
		t.Fatalf("after the token expired: %v", err)
	}
	if n := srv.TokenCalls(); n != 2 {
		t.Fatalf("%d tokens asked for, want 2 (one new after it expired)", n)
	}
	other := paymobtest.Credentials()
	other.APIKey = "another-merchant"
	if _, err := g.FetchStatus(t.Context(), other, req.Reference, money.EGP); err == nil {
		t.Fatal("another merchant's key used the first merchant's token")
	}
}
