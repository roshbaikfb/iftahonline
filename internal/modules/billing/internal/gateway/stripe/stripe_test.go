package stripe_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/gateway/stripe"
	platformstripe "github.com/roshbaikfb/iftahonline/internal/platform/stripe"
)

// standIn is Stripe's API as the adapter uses it: PaymentIntents found by
// their reference, created off session (a card "pm_declined" is
// declined, "pm_3ds" asks for authentication, "pm_slow" is processing),
// Checkout Sessions created and read back.
type standIn struct {
	mu       sync.Mutex
	intents  map[string]map[string]any // by reference
	sessions map[string]map[string]any // by ID
	forms    []map[string]string
	keys     []string
	down     bool
}

func newStandIn(t *testing.T) (*standIn, *stripe.Gateway, *time.Time) {
	t.Helper()
	s := &standIn{intents: map[string]map[string]any{}, sessions: map[string]map[string]any{}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	now := time.Unix(1_800_000_000, 0)
	g := stripe.New(stripe.Options{SecretKey: "rk_test_1", WebhookSecret: "whsec_1", BaseURL: srv.URL, Client: srv.Client(),
		Now: func() time.Time { return now }})
	return s, g, &now
}

func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("Authorization") != "Bearer rk_test_1" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	form := map[string]string{}
	for k, v := range r.Form {
		form[k] = v[0]
	}
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/payment_intents/search":
		ref := strings.TrimSuffix(strings.TrimPrefix(form["query"], "metadata['reference']:'"), "'")
		data := []any{}
		if pi, ok := s.intents[ref]; ok {
			data = append(data, pi)
		}
		reply(http.StatusOK, map[string]any{"data": data})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/payment_intents":
		s.forms, s.keys = append(s.forms, form), append(s.keys, r.Header.Get("Idempotency-Key"))
		ref := form["metadata[reference]"]
		amount := json.Number(form["amount"])
		pi := map[string]any{"id": "pi_" + ref, "amount": amount, "currency": form["currency"], "metadata": map[string]string{"reference": ref}}
		switch form["payment_method"] {
		case "pm_declined":
			pi["status"] = "requires_payment_method"
			s.intents[ref] = pi
			reply(http.StatusPaymentRequired, map[string]any{"error": map[string]any{"type": "card_error", "code": "card_declined",
				"decline_code": "insufficient_funds", "message": "Your card has insufficient funds.", "payment_intent": pi}})
			return
		case "pm_3ds":
			pi["status"] = "requires_action"
		case "pm_slow":
			pi["status"] = "processing"
		default:
			pi["status"], pi["amount_received"] = "succeeded", amount
		}
		s.intents[ref] = pi
		reply(http.StatusOK, pi)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		s.forms, s.keys = append(s.forms, form), append(s.keys, r.Header.Get("Idempotency-Key"))
		id := "cs_test_" + strings.TrimPrefix(form["client_reference_id"], "bc_")
		s.sessions[id] = map[string]any{"id": id, "object": "checkout.session", "client_reference_id": form["client_reference_id"],
			"status": "open", "payment_status": "unpaid", "amount_total": json.Number(form["line_items[0][price_data][unit_amount]"]),
			"currency": form["line_items[0][price_data][currency]"]}
		reply(http.StatusOK, map[string]any{"id": id, "url": "https://checkout.stripe.com/c/pay/" + id})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		cs, ok := s.sessions[strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")]
		if !ok || form["expand[]"] != "payment_intent.payment_method" {
			reply(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "No such checkout.session"}})
			return
		}
		reply(http.StatusOK, cs)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// pay completes a session as Stripe would after the merchant paid on it,
// saving the card.
func (s *standIn) pay(id string, received int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.sessions[id]
	cs["status"], cs["payment_status"], cs["customer"] = "complete", "paid", "cus_1"
	cs["payment_intent"] = map[string]any{"id": "pi_cs", "status": "succeeded", "amount_received": received,
		"payment_method": map[string]any{"id": "pm_1", "card": map[string]any{"brand": "visa", "last4": "4242"}}}
}

// TestCharge: an off-session charge is approved, declined (with Stripe's
// reason) or asks for the merchant (3DS); an attempt Stripe already has
// is read back, never charged twice; a pending one is not known yet.
func TestCharge(t *testing.T) {
	t.Parallel()
	s, g, _ := newStandIn(t)
	card := stripe.Token("cus_1", "pm_1")
	res, err := g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-1"})
	if err != nil || !res.Approved || res.Reference != "pi_INV-1-1" {
		t.Fatalf("charge = %+v, %v", res, err)
	}
	f := s.forms[0]
	if f["off_session"] != "true" || f["confirm"] != "true" || f["customer"] != "cus_1" || f["payment_method"] != "pm_1" || f["currency"] != "usd" ||
		f["amount"] != "2900" || s.keys[0] != "charge:INV-1-1" {
		t.Fatalf("request = %v %v", f, s.keys)
	}
	// A retry of the same attempt finds it: no second charge.
	if res, err = g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-1"}); err != nil || !res.Approved || len(s.forms) != 1 {
		t.Fatalf("retry = %+v, %v (%d requests)", res, err, len(s.forms))
	}
	res, err = g.Charge(t.Context(), app.Charge{Token: stripe.Token("cus_1", "pm_declined"), AmountMinor: 2900, Currency: "USD", Reference: "INV-1-2"})
	if err != nil || res.Approved || res.Reason != "Your card has insufficient funds." || res.Reference != "pi_INV-1-2" {
		t.Fatalf("declined = %+v, %v", res, err)
	}
	// The declined attempt asked again reads its last error.
	s.intents["INV-1-2"]["last_payment_error"] = map[string]any{"code": "card_declined", "message": "Declined."}
	if res, err = g.Charge(t.Context(), app.Charge{Token: stripe.Token("cus_1", "pm_declined"), AmountMinor: 2900, Currency: "USD", Reference: "INV-1-2"}); err != nil ||
		res.Approved || res.Reason != "Declined." {
		t.Fatalf("declined, asked again = %+v, %v", res, err)
	}
	if res, err = g.Charge(t.Context(), app.Charge{Token: stripe.Token("cus_1", "pm_3ds"), AmountMinor: 2900, Currency: "USD", Reference: "INV-1-3"}); err != nil ||
		res.Approved || res.Reason != "authentication_required" {
		t.Fatalf("3DS = %+v, %v", res, err)
	}
	if _, err = g.Charge(t.Context(), app.Charge{Token: stripe.Token("cus_1", "pm_slow"), AmountMinor: 2900, Currency: "USD", Reference: "INV-1-4"}); err == nil {
		t.Fatal("a processing charge was known")
	}
	s.intents["INV-1-5"] = map[string]any{"id": "pi_x", "status": "canceled", "metadata": map[string]string{"reference": "INV-1-5"}}
	if res, err = g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-5"}); err != nil || res.Approved || res.Reason != "declined" {
		t.Fatalf("canceled = %+v, %v", res, err)
	}
	s.intents["INV-1-6"] = map[string]any{"id": "pi_y", "status": "succeeded", "amount_received": 100, "currency": "usd", "metadata": map[string]string{"reference": "INV-1-6"}}
	if _, err = g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-6"}); err == nil {
		t.Fatal("a partial amount approved")
	}
	s.intents["INV-1-7"] = map[string]any{"id": "pi_z", "status": "succeeded", "metadata": map[string]string{"reference": "someone-else"}}
	if _, err = g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-7"}); err == nil {
		t.Fatal("an answer about another payment believed")
	}
	for name, c := range map[string]app.Charge{
		"no card":            {AmountMinor: 1, Currency: "USD", Reference: "INV-1-8"},
		"not a Stripe card":  {Token: "tok-1", AmountMinor: 1, Currency: "USD", Reference: "INV-1-8"},
		"no amount":          {Token: card, Currency: "USD", Reference: "INV-1-8"},
		"a quote in the ref": {Token: card, AmountMinor: 1, Currency: "USD", Reference: "INV'1"},
		"no currency":        {Token: card, AmountMinor: 1, Reference: "INV-1-8"},
	} {
		if _, err = g.Charge(t.Context(), c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	s.down = true
	if _, err = g.Charge(t.Context(), app.Charge{Token: card, AmountMinor: 2900, Currency: "USD", Reference: "INV-1-9"}); err == nil {
		t.Fatal("Stripe down was an answer")
	}
}

// TestCheckoutAndPaid: a Checkout Session saves the card for off-session
// charges; Stripe is asked by the session, and only a session paid in
// full, in its currency, for our reference, is paid, with its card.
func TestCheckoutAndPaid(t *testing.T) {
	t.Parallel()
	s, g, _ := newStandIn(t)
	ref := "bc_01JABCDEFGHJKMNPQRSTVWXYZ0"
	page, err := g.Checkout(t.Context(), app.CheckoutRequest{Reference: ref, AmountMinor: 2900, Currency: "USD", Description: "INV-2026-10-0001",
		Email: "owner@example.com", RedirectURL: "https://app.example.com/stores/x/billing", NotificationURL: "ignored"})
	if err != nil || page.Session != "cs_test_01JABCDEFGHJKMNPQRSTVWXYZ0" || !strings.HasPrefix(page.URL, "https://checkout.stripe.com/") || page.ProviderOrderID != "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	f := s.forms[0]
	for k, want := range map[string]string{
		"mode": "payment", "client_reference_id": ref, "customer_creation": "always", "customer_email": "owner@example.com",
		"payment_intent_data[setup_future_usage]": "off_session", "payment_intent_data[metadata][reference]": ref,
		"line_items[0][price_data][currency]": "usd", "line_items[0][price_data][unit_amount]": "2900",
		"line_items[0][price_data][product_data][name]": "Iftah Online INV-2026-10-0001",
		"success_url": "https://app.example.com/stores/x/billing", "cancel_url": "https://app.example.com/stores/x/billing",
	} {
		if f[k] != want {
			t.Errorf("%s = %q, want %q", k, f[k], want)
		}
	}
	if s.keys[0] != "checkout:"+ref {
		t.Errorf("idempotency key = %q", s.keys[0])
	}
	q := app.PaidQuery{Currency: "USD", Reference: ref, Session: page.Session, AmountMinor: 2900}
	if p, pErr := g.Paid(t.Context(), q); pErr != nil || p.Paid || p.Token != "" {
		t.Fatalf("before paying = %+v, %v", p, pErr)
	}
	s.pay(page.Session, 2900)
	p, err := g.Paid(t.Context(), q)
	if err != nil || !p.Paid || p.TransactionID != "pi_cs" || p.Token != stripe.Token("cus_1", "pm_1") || p.Label != "VISA •••• 4242" {
		t.Fatalf("paid = %+v, %v", p, err)
	}
	if p, err = g.Paid(t.Context(), app.PaidQuery{Currency: "USD", Reference: ref, Session: page.Session, AmountMinor: 5000}); err != nil || p.Paid || p.Token != "" {
		t.Fatalf("another amount = %+v, %v", p, err)
	}
	if _, err = g.Paid(t.Context(), app.PaidQuery{Currency: "SAR", Reference: ref, Session: page.Session, AmountMinor: 2900}); err == nil {
		t.Fatal("another currency believed")
	}
	if _, err = g.Paid(t.Context(), app.PaidQuery{Currency: "USD", Reference: "bc_other", Session: page.Session, AmountMinor: 2900}); err == nil {
		t.Fatal("another reference believed")
	}
	if p, err = g.Paid(t.Context(), app.PaidQuery{Currency: "USD", Reference: ref, Session: "cs_test_gone", AmountMinor: 2900}); err != nil || p.Paid {
		t.Fatalf("an unknown session = %+v, %v", p, err)
	}
	if _, err = g.Paid(t.Context(), app.PaidQuery{Currency: "USD", Reference: ref, AmountMinor: 2900}); err == nil {
		t.Fatal("asked without a session")
	}

	// A malformed email is left out; a bad request is refused.
	if _, err = g.Checkout(t.Context(), app.CheckoutRequest{Reference: "bc_2", AmountMinor: 100, Currency: "USD", Email: "Owner <o@example.com>",
		RedirectURL: "https://app.example.com"}); err != nil || s.forms[1]["customer_email"] != "" {
		t.Fatalf("email = %q, %v", s.forms[1]["customer_email"], err)
	}
	for name, c := range map[string]app.CheckoutRequest{
		"no amount":   {Reference: "bc_3", Currency: "USD", RedirectURL: "https://a"},
		"no return":   {Reference: "bc_3", AmountMinor: 1, Currency: "USD"},
		"a bad ref":   {Reference: "bc 3", AmountMinor: 1, Currency: "USD", RedirectURL: "https://a"},
		"no currency": {Reference: "bc_3", AmountMinor: 1, RedirectURL: "https://a"},
	} {
		if _, err = g.Checkout(t.Context(), c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	s.down = true
	if _, err = g.Checkout(t.Context(), app.CheckoutRequest{Reference: "bc_4", AmountMinor: 1, Currency: "USD", RedirectURL: "https://a"}); err == nil {
		t.Fatal("Stripe down opened a page")
	}
	if _, err = g.Paid(t.Context(), q); err == nil {
		t.Fatal("Stripe down answered")
	}
}

func event(typ, object string) []byte {
	return []byte(`{"id":"evt_1","type":"` + typ + `","data":{"object":` + object + `}}`)
}

// TestParseCallback: an event is believed only with the endpoint's
// recent signature, and read only when it is about one of our sessions.
func TestParseCallback(t *testing.T) {
	t.Parallel()
	_, g, now := newStandIn(t)
	body := event("checkout.session.completed", `{"object":"checkout.session","id":"cs_1","client_reference_id":"bc_01J"}`)
	signed := http.Header{"Stripe-Signature": {platformstripe.Sign("whsec_1", body, *now)}}
	cb, err := g.ParseCallback(signed, nil, body)
	if err != nil || cb.Kind != app.CallbackTransaction || cb.Reference != "bc_01J" || cb.EventID != "evt_1" {
		t.Fatalf("callback = %+v, %v", cb, err)
	}
	for name, h := range map[string]http.Header{
		"unsigned":       {},
		"another secret": {"Stripe-Signature": {platformstripe.Sign("whsec_2", body, *now)}},
		"replayed":       {"Stripe-Signature": {platformstripe.Sign("whsec_1", body, now.Add(-time.Hour))}},
	} {
		if _, err = g.ParseCallback(h, nil, body); !errors.Is(err, app.ErrForgedCallback) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, b := range map[string][]byte{
		"another event":     event("payment_intent.succeeded", `{"object":"payment_intent","id":"pi_1"}`),
		"not our session":   event("checkout.session.completed", `{"object":"checkout.session","id":"cs_1","client_reference_id":"order-9"}`),
		"no reference":      event("checkout.session.expired", `{"object":"checkout.session","id":"cs_1"}`),
		"another object":    event("checkout.session.completed", `{"object":"invoice","id":"in_1","client_reference_id":"bc_01J"}`),
		"not an event":      []byte(`{}`),
		"an unreadable one": event("checkout.session.completed", `"x"`),
	} {
		if _, err = g.ParseCallback(http.Header{"Stripe-Signature": {platformstripe.Sign("whsec_1", b, *now)}}, nil, b); !errors.Is(err, app.ErrBadCallback) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzReadCallback: any body is read without panicking; a callback it
// accepts names one of our checkouts.
func FuzzReadCallback(f *testing.F) {
	f.Add(event("checkout.session.completed", `{"object":"checkout.session","id":"cs_1","client_reference_id":"bc_01J"}`))
	f.Add(event("checkout.session.expired", `{"object":"checkout.session"}`))
	f.Add([]byte(`{"id":"evt_1"}`))
	g := stripe.New(stripe.Options{})
	f.Fuzz(func(t *testing.T, body []byte) {
		cb, err := g.ReadCallback(body)
		if err == nil && (!strings.HasPrefix(cb.Reference, "bc_") || cb.Kind != app.CallbackTransaction || !strings.HasPrefix(cb.EventID, "evt_")) {
			t.Fatalf("accepted %q as %+v", body, cb)
		}
	})
}
