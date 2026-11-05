package stripe_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/gatewaytest"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/stripe"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	platformstripe "github.com/roshbaikfb/iftahonline/internal/platform/stripe"
)

var fast = retry.Policy{MaxAttempts: 3, Initial: time.Millisecond, Max: 2 * time.Millisecond}

const (
	secretKey = "rk_test_merchant"
	whsec     = "whsec_merchant"
)

func creds() gateway.Credentials {
	return gateway.Credentials{SecretKey: secretKey, WebhookSecret: whsec}
}

// standIn is a merchant's Stripe account as the adapter uses it:
// Checkout Sessions created (once per idempotency key) and read back
// with their PaymentIntent and latest charge, refunds on a
// PaymentIntent within what it received.
type standIn struct {
	mu       sync.Mutex
	sessions map[string]map[string]any // by ID
	byKey    map[string]string         // idempotency key → session ID
	forms    []map[string]string
	n        int
	down     bool
	now      time.Time
}

func newStandIn(t *testing.T) (*standIn, *stripe.Gateway) {
	t.Helper()
	s := &standIn{sessions: map[string]map[string]any{}, byKey: map[string]string{}, now: time.Unix(1_800_000_000, 0)}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	g := stripe.New(stripe.Options{BaseURL: srv.URL, Client: srv.Client(), Retry: &fast, Now: func() time.Time { return s.now }})
	return s, g
}

func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if s.down {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+secretKey {
		reply(http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "Invalid API Key provided"}})
		return
	}
	_ = r.ParseForm()
	form := map[string]string{}
	for k, v := range r.Form {
		form[k] = v[0]
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions":
		reply(http.StatusOK, map[string]any{"data": []any{}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		s.forms = append(s.forms, form)
		if id, ok := s.byKey[r.Header.Get("Idempotency-Key")]; ok {
			reply(http.StatusOK, map[string]any{"id": id, "url": "https://checkout.stripe.com/c/pay/" + id})
			return
		}
		if form["client_reference_id"] == "" || form["line_items[0][price_data][unit_amount]"] == "" {
			reply(http.StatusBadRequest, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "Missing required param"}})
			return
		}
		s.n++
		id := "cs_test_" + strconv.Itoa(s.n)
		s.byKey[r.Header.Get("Idempotency-Key")] = id
		amount, _ := strconv.ParseInt(form["line_items[0][price_data][unit_amount]"], 10, 64)
		s.sessions[id] = map[string]any{"id": id, "object": "checkout.session", "client_reference_id": form["client_reference_id"],
			"status": "open", "payment_status": "unpaid", "amount_total": amount, "currency": form["line_items[0][price_data][currency]"],
			"payment_intent": nil}
		reply(http.StatusOK, map[string]any{"id": id, "url": "https://checkout.stripe.com/c/pay/" + id})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		cs, ok := s.sessions[strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")]
		if !ok {
			reply(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "No such checkout.session"}})
			return
		}
		if form["expand[]"] != "payment_intent.latest_charge" {
			reply(http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "not expanded"}})
			return
		}
		reply(http.StatusOK, cs)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
		s.forms = append(s.forms, form)
		amount, _ := strconv.ParseInt(form["amount"], 10, 64)
		for _, cs := range s.sessions {
			pi, _ := cs["payment_intent"].(map[string]any)
			if pi == nil || pi["id"] != form["payment_intent"] {
				continue
			}
			charge := pi["latest_charge"].(map[string]any)
			refunded := charge["amount_refunded"].(int64)
			if refunded+amount > pi["amount_received"].(int64) {
				reply(http.StatusBadRequest, map[string]any{"error": map[string]any{"type": "invalid_request_error",
					"message": "Refund amount is greater than unrefunded amount on charge"}})
				return
			}
			charge["amount_refunded"] = refunded + amount
			s.n++
			reply(http.StatusOK, map[string]any{"id": "re_" + strconv.Itoa(s.n), "status": "succeeded"})
			return
		}
		reply(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "No such payment_intent"}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *standIn) byReference(ref string) map[string]any {
	for _, cs := range s.sessions {
		if cs["client_reference_id"] == ref {
			return cs
		}
	}
	return nil
}

// pay is the shopper paying the reference's page.
func (s *standIn) pay(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.byReference(ref)
	if cs == nil {
		return fmt.Errorf("no session for %s", ref)
	}
	cs["status"], cs["payment_status"] = "complete", "paid"
	cs["payment_intent"] = map[string]any{"id": "pi_" + cs["id"].(string), "status": "succeeded", "amount_received": cs["amount_total"],
		"latest_charge": map[string]any{"amount_refunded": int64(0), "created": s.now.Unix()}}
	return nil
}

// decline is the shopper leaving after the card was declined: the page
// expires unpaid.
func (s *standIn) decline(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.byReference(ref)
	if cs == nil {
		return fmt.Errorf("no session for %s", ref)
	}
	cs["status"] = "expired"
	return nil
}

// webhook is the signed event Stripe sends about the reference's session.
func (s *standIn) webhook(ref string) (gateway.WebhookRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.byReference(ref)
	if cs == nil {
		return gateway.WebhookRequest{}, fmt.Errorf("no session for %s", ref)
	}
	body, err := json.Marshal(map[string]any{"id": "evt_" + cs["id"].(string), "type": "checkout.session.completed", "data": map[string]any{"object": cs}})
	if err != nil {
		return gateway.WebhookRequest{}, err
	}
	return gateway.WebhookRequest{Header: http.Header{"Stripe-Signature": {platformstripe.Sign(whsec, body, s.now)}}, Body: body}, nil
}

func TestContract(t *testing.T) {
	t.Parallel()
	s, g := newStandIn(t)
	bad := creds()
	bad.SecretKey = "rk_test_wrong"
	gatewaytest.Run(t, gatewaytest.Harness{
		Gateway: g, Creds: creds(), BadCreds: bad, Currency: money.USD,
		Pay: func(t *testing.T, ref string) {
			t.Helper()
			if err := s.pay(ref); err != nil {
				t.Fatal(err)
			}
		},
		Decline: func(t *testing.T, ref string) {
			t.Helper()
			if err := s.decline(ref); err != nil {
				t.Fatal(err)
			}
		},
		Webhook: func(t *testing.T, ref string) gateway.WebhookRequest {
			t.Helper()
			w, err := s.webhook(ref)
			if err != nil {
				t.Fatal(err)
			}
			return w
		},
	})
}

// TestSandboxContract runs the suite against Stripe's test mode with a
// merchant's test keys (STRIPE_SANDBOX_SECRET_KEY, _WEBHOOK_SECRET): the
// check that the shapes are Stripe's. Paying needs a person on the
// hosted page, so those cases are left out.
func TestSandboxContract(t *testing.T) {
	sk := os.Getenv("STRIPE_SANDBOX_SECRET_KEY")
	if sk == "" {
		t.Skip("STRIPE_SANDBOX_* not set: Stripe's sandbox contract is not run")
	}
	c := gateway.Credentials{SecretKey: sk, WebhookSecret: os.Getenv("STRIPE_SANDBOX_WEBHOOK_SECRET")}
	bad := c
	bad.SecretKey = "sk_test_invalid"
	gatewaytest.Run(t, gatewaytest.Harness{Gateway: stripe.New(stripe.Options{}), Creds: c, BadCreds: bad, Currency: money.USD})
}

// TestCreatePayment: the session Stripe receives, one per reference.
func TestCreatePayment(t *testing.T) {
	t.Parallel()
	s, g := newStandIn(t)
	req := gatewaytest.Request(t, money.SAR)
	req.Customer.Email = "mona@example.com"
	c, err := g.CreatePayment(t.Context(), creds(), req)
	if err != nil || c.ProviderRef != "cs_test_1" || !strings.HasPrefix(c.URL, "https://checkout.stripe.com/") {
		t.Fatalf("checkout = %+v, %v", c, err)
	}
	f := s.forms[0]
	for k, want := range map[string]string{
		"mode": "payment", "client_reference_id": req.Reference, "metadata[reference]": req.Reference,
		"payment_intent_data[metadata][reference]": req.Reference, "customer_email": "mona@example.com",
		"line_items[0][price_data][currency]": "sar", "line_items[0][price_data][unit_amount]": "52000",
		"line_items[0][price_data][product_data][name]": "Order 7P0R1S2T",
		"success_url": req.RedirectURL, "cancel_url": req.RedirectURL, "payment_method_types[0]": "card",
		"expires_at": strconv.FormatInt(s.now.Add(stripe.SessionTTL).Unix(), 10),
	} {
		if f[k] != want {
			t.Errorf("%s = %q, want %q", k, f[k], want)
		}
	}
	if _, ok := f["setup_future_usage"]; ok {
		t.Error("a shopper's card saved")
	}
	// Sent again (a lost answer): the same session.
	if again, aErr := g.CreatePayment(t.Context(), creds(), req); aErr != nil || again.ProviderRef != c.ProviderRef {
		t.Fatalf("again = %+v, %v", again, aErr)
	}
	// A display name in the email is left out; no description names the
	// reference.
	req = gatewaytest.Request(t, money.USD)
	req.Customer.Email, req.Description = "Mona <mona@example.com>", ""
	if _, err = g.CreatePayment(t.Context(), creds(), req); err != nil || s.forms[2]["customer_email"] != "" ||
		s.forms[2]["line_items[0][price_data][product_data][name]"] != req.Reference {
		t.Fatalf("email %q, name %q, %v", s.forms[2]["customer_email"], s.forms[2]["line_items[0][price_data][product_data][name]"], err)
	}
	for name, mod := range map[string]func(*gateway.PaymentRequest){
		"no return URL":    func(r *gateway.PaymentRequest) { r.RedirectURL = "" },
		"a quote in a ref": func(r *gateway.PaymentRequest) { r.Reference = "x'y" },
	} {
		r := gatewaytest.Request(t, money.USD)
		mod(&r)
		if _, err = g.CreatePayment(t.Context(), creds(), r); !errors.Is(err, gateway.ErrRejected) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = g.CreatePayment(t.Context(), gateway.Credentials{SecretKey: "pk_test_1", WebhookSecret: whsec}, gatewaytest.Request(t, money.USD)); !errors.Is(err, gateway.ErrAuth) {
		t.Errorf("a publishable key: %v", err)
	}
	s.down = true
	if _, err = g.CreatePayment(t.Context(), creds(), gatewaytest.Request(t, money.USD)); err == nil || errors.Is(err, gateway.ErrRejected) {
		t.Errorf("Stripe down: %v", err)
	}
}

// TestVerifyKeys: only Stripe's secret or restricted key with an
// endpoint secret, accepted by Stripe.
func TestVerifyKeys(t *testing.T) {
	t.Parallel()
	s, g := newStandIn(t)
	if k := g.NeedsKeys(); !k.SecretKey || !k.WebhookSecret || k.APIKey || k.PublicKey {
		t.Fatalf("keys = %+v", k)
	}
	if gateway.RequiredKeys(g) != g.NeedsKeys() {
		t.Fatal("RequiredKeys")
	}
	for name, c := range map[string]gateway.Credentials{
		"a publishable key":    {SecretKey: "pk_live_1", WebhookSecret: whsec},
		"no endpoint secret":   {SecretKey: secretKey},
		"a Paymob HMAC":        {SecretKey: secretKey, WebhookSecret: "ABCDEF"},
		"a key Stripe refuses": {SecretKey: "sk_live_wrong", WebhookSecret: whsec},
	} {
		if err := g.Verify(t.Context(), c); !errors.Is(err, gateway.ErrAuth) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s.down = true
	if err := g.Verify(t.Context(), creds()); err == nil || errors.Is(err, gateway.ErrAuth) {
		t.Errorf("Stripe down: %v", err)
	}
}

// TestFetchStatus: the answer is believed only for the reference and the
// currency asked; a payment without a session is pending.
func TestFetchStatus(t *testing.T) {
	t.Parallel()
	s, g := newStandIn(t)
	req := gatewaytest.Request(t, money.USD)
	c, err := g.CreatePayment(t.Context(), creds(), req)
	if err != nil {
		t.Fatal(err)
	}
	if st, sErr := g.FetchStatus(t.Context(), creds(), gateway.Ref{Reference: req.Reference}, money.USD); sErr != nil || st.State != gateway.StatePending {
		t.Fatalf("no session = %+v, %v", st, sErr)
	}
	if err = s.pay(req.Reference); err != nil {
		t.Fatal(err)
	}
	ref := gateway.Ref{Reference: req.Reference, ProviderRef: c.ProviderRef}
	st, err := g.FetchStatus(t.Context(), creds(), ref, money.USD)
	if err != nil || st.State != gateway.StatePaid || st.TransactionID != "pi_"+c.ProviderRef || !st.At.Equal(s.now) {
		t.Fatalf("paid = %+v, %v", st, err)
	}
	for name, tc := range map[string]struct {
		ref gateway.Ref
		cur money.Currency
	}{
		"another reference":   {gateway.Ref{Reference: "01OTHER", ProviderRef: c.ProviderRef}, money.USD},
		"another currency":    {ref, money.SAR},
		"not a session":       {gateway.Ref{Reference: req.Reference, ProviderRef: "pi_1"}, money.USD},
		"another account's":   {gateway.Ref{Reference: req.Reference, ProviderRef: "cs_test_404"}, money.USD},
		"an unknown currency": {ref, money.Currency("XXX")},
	} {
		if _, err = g.FetchStatus(t.Context(), creds(), tc.ref, tc.cur); err == nil {
			t.Errorf("%s answered", name)
		}
	}
	if _, err = g.Refund(t.Context(), creds(), "ch_1", st.Amount); !errors.Is(err, gateway.ErrRejected) {
		t.Errorf("a refund of a charge: %v", err)
	}
	if _, err = g.Refund(t.Context(), creds(), "pi_none", st.Amount); !errors.Is(err, gateway.ErrRejected) {
		t.Errorf("a refund of nothing: %v", err)
	}
	s.down = true
	if _, err = g.FetchStatus(t.Context(), creds(), ref, money.USD); err == nil {
		t.Error("Stripe down answered")
	}
	if _, err = g.Refund(t.Context(), creds(), st.TransactionID, st.Amount); err == nil || errors.Is(err, gateway.ErrRejected) {
		t.Errorf("a refund while Stripe is down: %v", err)
	}
}

func event(typ, object string) []byte {
	return []byte(`{"id":"evt_1","type":"` + typ + `","data":{"object":` + object + `}}`)
}

// TestWebhooks: an event is believed with the merchant's endpoint secret
// only, recent, and read only about one of our sessions.
func TestWebhooks(t *testing.T) {
	t.Parallel()
	s, g := newStandIn(t)
	body := event("checkout.session.completed", `{"object":"checkout.session","id":"cs_1","client_reference_id":"01JREF"}`)
	signed := gateway.WebhookRequest{Header: http.Header{"Stripe-Signature": {platformstripe.Sign(whsec, body, s.now)}}, Body: body}
	if ev, err := g.VerifyWebhook(creds(), signed); err != nil || ev.Reference != "01JREF" || ev.EventID != "evt_1" {
		t.Fatalf("event = %+v, %v", ev, err)
	}
	old := signed
	old.Header = http.Header{"Stripe-Signature": {platformstripe.Sign(whsec, body, s.now.Add(-time.Hour))}}
	if _, err := g.VerifyWebhook(creds(), old); !errors.Is(err, gateway.ErrForged) {
		t.Errorf("replayed: %v", err)
	}
	for name, b := range map[string][]byte{
		"a charge's event":  event("charge.succeeded", `{"object":"charge","id":"ch_1"}`),
		"no reference":      event("checkout.session.expired", `{"object":"checkout.session","id":"cs_1"}`),
		"another object":    event("checkout.session.completed", `{"object":"invoice","client_reference_id":"01JREF"}`),
		"not an event":      []byte(`[]`),
		"an unreadable one": event("checkout.session.completed", `1`),
	} {
		if _, err := g.ParseWebhook(b); !errors.Is(err, gateway.ErrBadWebhook) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzParseWebhook: any body is read without panicking; an event it
// accepts names a reference.
func FuzzParseWebhook(f *testing.F) {
	f.Add(event("checkout.session.completed", `{"object":"checkout.session","client_reference_id":"01JREF"}`))
	f.Add([]byte(`{"id":"evt_","type":"checkout.session.expired","data":{"object":{}}}`))
	g := stripe.New(stripe.Options{})
	f.Fuzz(func(t *testing.T, body []byte) {
		ev, err := g.ParseWebhook(body)
		if err == nil && (ev.Reference == "" || !strings.HasPrefix(ev.EventID, "evt_")) {
			t.Fatalf("accepted %q as %+v", body, ev)
		}
	})
}
