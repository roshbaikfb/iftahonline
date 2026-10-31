package paymob_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/gateway/paymob"
	platformpaymob "github.com/roshbaikfb/iftahonline/internal/platform/paymob"
)

// standIn is Paymob as the "Pay with saved card" collection describes
// it: auth tokens, inquiries by merchant order, intentions, token pays.
type standIn struct {
	mu        sync.Mutex
	txns      map[string]map[string]any // by special_reference
	keys      map[string]string         // payment key -> special_reference
	pays      int
	loseReply bool // the next pay is made but its answer is lost
	intention map[string]any
}

func newStandIn(t *testing.T) (*standIn, *httptest.Server) {
	t.Helper()
	s := &standIn{txns: map[string]map[string]any{}, keys: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *standIn) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	switch r.URL.Path {
	case "/api/auth/tokens":
		if in["api_key"] != "api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "bearer-1"})
	case "/api/ecommerce/orders/transaction_inquiry":
		if r.Header.Get("Authorization") != "Bearer bearer-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		t, ok := s.txns[in["merchant_order_id"].(string)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(t)
	case "/v1/intention/":
		if r.Header.Get("Authorization") != "Token secret-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.intention = in
		ref := in["special_reference"].(string)
		if _, used := s.txns[ref]; used {
			w.WriteHeader(http.StatusBadRequest) // Paymob refuses a reused special_reference
			return
		}
		key := "pk-" + ref
		s.keys[key] = ref
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_1", "client_secret": "pak_csk_" + ref, "intention_order_id": 4547,
			"payment_keys": []map[string]any{{"key": key, "integration": 777}}})
	case "/api/acceptance/payments/pay":
		ref := s.keys[in["payment_token"].(string)]
		src := in["source"].(map[string]any)
		ok := !strings.HasPrefix(src["identifier"].(string), "decline") && src["subtype"] == "TOKEN"
		code := "APPROVED"
		if !ok {
			code = "DECLINED"
		}
		s.pays++
		t := map[string]any{"id": 900 + s.pays, "success": ok, "pending": false, "txn_response_code": code,
			"data":  map[string]any{"message": map[bool]string{true: "Approved", false: "Insufficient Funds"}[ok]},
			"order": map[string]any{"merchant_order_id": ref}}
		s.txns[ref] = t
		if s.loseReply {
			s.loseReply = false
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(t)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func gateway(srv *httptest.Server) *paymob.Gateway {
	return paymob.New(paymob.Options{BaseURL: srv.URL, Regions: map[string]paymob.Region{
		"EGP": {SecretKey: "secret-key", APIKey: "api-key", MotoIntegrationID: 777},
	}})
}

func TestCharge(t *testing.T) {
	t.Parallel()
	s, srv := newStandIn(t)
	g := gateway(srv)
	res, err := g.Charge(t.Context(), app.Charge{Token: "card-1", AmountMinor: 68286, Currency: "EGP", Reference: "INV-2026-000001-1"})
	if err != nil || !res.Approved || res.Reference != "901" {
		t.Fatalf("approved = %+v, %v", res, err)
	}
	if s.intention["amount"] != float64(68286) || s.intention["payment_methods"].([]any)[0] != float64(777) || s.intention["currency"] != "EGP" {
		t.Errorf("intention = %+v", s.intention)
	}
	res, err = g.Charge(t.Context(), app.Charge{Token: "decline-1", AmountMinor: 100, Currency: "EGP", Reference: "INV-2026-000002-1"})
	if err != nil || res.Approved || res.Reason != "Insufficient Funds" {
		t.Fatalf("declined = %+v, %v", res, err)
	}
	// The same attempt again (a retried job) is found, not charged.
	before := s.pays
	if res, err = g.Charge(t.Context(), app.Charge{Token: "card-1", AmountMinor: 68286, Currency: "EGP", Reference: "INV-2026-000001-1"}); err != nil || !res.Approved || s.pays != before {
		t.Fatalf("repeat = %+v, %v, %d pays", res, err, s.pays-before)
	}
}

// TestChargeWithALostAnswer: the pay went through but its answer was
// lost; the retried job finds it by its reference and pays once.
func TestChargeWithALostAnswer(t *testing.T) {
	t.Parallel()
	s, srv := newStandIn(t)
	g := gateway(srv)
	s.loseReply = true
	c := app.Charge{Token: "card-1", AmountMinor: 5000, Currency: "EGP", Reference: "INV-2026-000003-1"}
	if _, err := g.Charge(t.Context(), c); err == nil {
		t.Fatal("a lost answer: want an error (the job retries)")
	}
	res, err := g.Charge(t.Context(), c)
	if err != nil || !res.Approved || s.pays != 1 {
		t.Fatalf("after the retry: %+v, %v, %d pays", res, err, s.pays)
	}
}

func TestChargeRefusals(t *testing.T) {
	t.Parallel()
	_, srv := newStandIn(t)
	g := gateway(srv)
	for name, c := range map[string]app.Charge{
		"no region":    {Token: "t", AmountMinor: 1, Currency: "SAR", Reference: "r"},
		"no token":     {AmountMinor: 1, Currency: "EGP", Reference: "r"},
		"no amount":    {Token: "t", Currency: "EGP", Reference: "r"},
		"no reference": {Token: "t", AmountMinor: 1, Currency: "EGP"},
	} {
		if _, err := g.Charge(t.Context(), c); err == nil {
			t.Errorf("%s: charged", name)
		}
	}
	bad := paymob.New(paymob.Options{BaseURL: srv.URL, Regions: map[string]paymob.Region{"EGP": {SecretKey: "secret-key", APIKey: "wrong", MotoIntegrationID: 777}}})
	if _, err := bad.Charge(t.Context(), app.Charge{Token: "t", AmountMinor: 1, Currency: "EGP", Reference: "r"}); err == nil {
		t.Error("a refused API key charged")
	}
}

func checkoutGateway(srv *httptest.Server) *paymob.Gateway {
	return paymob.New(paymob.Options{BaseURL: srv.URL, Regions: map[string]paymob.Region{
		"EGP": {SecretKey: "secret-key", APIKey: "api-key", MotoIntegrationID: 777, PublicKey: "egy_pk", CardIntegrationID: 555, HMACSecret: "hmac-eg"},
		"SAR": {SecretKey: "secret-sa", APIKey: "api-sa", MotoIntegrationID: 778, PublicKey: "sa_pk", CardIntegrationID: 556, HMACSecret: "hmac-sa"},
	}})
}

func TestCheckout(t *testing.T) {
	t.Parallel()
	s, srv := newStandIn(t)
	g := checkoutGateway(srv)
	page, err := g.Checkout(t.Context(), app.CheckoutRequest{Reference: "bc_01", AmountMinor: 68286, Currency: "EGP", Description: "INV-2026-000001",
		NotificationURL: "https://api.example.com/v1/webhooks/paymob_billing", RedirectURL: "https://app.example.com/stores/x/billing"})
	if err != nil || !strings.Contains(page.URL, "/unifiedcheckout/?publicKey=egy_pk&clientSecret=pak_csk_bc_01") || page.ProviderOrderID != "4547" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if s.intention["payment_methods"].([]any)[0] != float64(555) || s.intention["notification_url"] != "https://api.example.com/v1/webhooks/paymob_billing" ||
		s.intention["special_reference"] != "bc_01" {
		t.Errorf("intention = %+v", s.intention)
	}
	if _, err = g.Checkout(t.Context(), app.CheckoutRequest{Reference: "bc_02", AmountMinor: 1, Currency: "AED"}); !errors.Is(err, app.ErrNoCheckout) {
		t.Errorf("no checkout in the UAE: %v", err)
	}
}

func signed(t *testing.T, secret string, body string, fields []string) url.Values {
	t.Helper()
	mac, err := platformpaymob.Sign(secret, []byte(body), fields)
	if err != nil {
		t.Fatal(err)
	}
	return url.Values{"hmac": {mac}}
}

func TestParseCallback(t *testing.T) {
	t.Parallel()
	_, srv := newStandIn(t)
	g := checkoutGateway(srv)
	txn := `{"type":"TRANSACTION","obj":{"id":9001,"success":true,"pending":false,"amount_cents":68286,"currency":"EGP","order":{"id":4547,"merchant_order_id":"bc_01"}}}`
	token := `{"type":"TOKEN","obj":{"id":12929738,"token":"tok-1","masked_pan":"xxxx-xxxx-xxxx-2346","merchant_id":1,"card_subtype":"MasterCard","created_at":"2026-01-20","email":"m@example.com","order_id":"4547"}}` // nocarddata:allow Paymob's masked card

	cb, err := g.ParseCallback(signed(t, "hmac-eg", txn, platformpaymob.TransactionFields()), []byte(txn))
	if err != nil || cb.Kind != app.CallbackTransaction || cb.Reference != "bc_01" || cb.ProviderOrderID != "4547" || cb.TransactionID != "9001" ||
		cb.EventID != "txn:9001:true:false" {
		t.Fatalf("transaction = %+v, %v", cb, err)
	}
	// Another region's secret is the platform's too.
	if _, err = g.ParseCallback(signed(t, "hmac-sa", txn, platformpaymob.TransactionFields()), []byte(txn)); err != nil {
		t.Errorf("Saudi secret: %v", err)
	}
	cb, err = g.ParseCallback(signed(t, "hmac-eg", token, platformpaymob.TokenFields()), []byte(token))
	if err != nil || cb.Kind != app.CallbackToken || cb.Token != "tok-1" || cb.ProviderOrderID != "4547" || cb.Label != "MasterCard •••• 2346" {
		t.Fatalf("token = %+v, %v", cb, err)
	}
	for name, q := range map[string]url.Values{
		"unsigned": {}, "a stranger's secret": signed(t, "other", txn, platformpaymob.TransactionFields()),
		"the token's fields": signed(t, "hmac-eg", txn, platformpaymob.TokenFields()),
	} {
		if _, err = g.ParseCallback(q, []byte(txn)); !errors.Is(err, app.ErrForgedCallback) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = g.ParseCallback(url.Values{}, []byte(`garbage`)); !errors.Is(err, app.ErrForgedCallback) {
		t.Errorf("garbage: %v", err)
	}
	if _, err = g.ReadCallback([]byte(`{"type":"TRANSACTION","obj":{"id":1}}`)); !errors.Is(err, app.ErrBadCallback) {
		t.Errorf("a transaction without our reference: %v", err)
	}
	if _, err = g.ReadCallback([]byte(`{"type":"TOKEN","obj":{"id":1}}`)); !errors.Is(err, app.ErrBadCallback) {
		t.Errorf("a token without its order: %v", err)
	}
}

func TestPaid(t *testing.T) {
	t.Parallel()
	s, srv := newStandIn(t)
	g := checkoutGateway(srv)
	s.txns["bc_paid"] = map[string]any{"id": 77, "success": true, "pending": false, "amount_cents": 68286, "currency": "EGP", "order": map[string]any{"merchant_order_id": "bc_paid"}}
	s.txns["bc_short"] = map[string]any{"id": 78, "success": true, "amount_cents": 100, "currency": "EGP", "order": map[string]any{"merchant_order_id": "bc_short"}}
	s.txns["bc_nocur"] = map[string]any{"id": 80, "success": true, "amount_cents": 68286, "order": map[string]any{"merchant_order_id": "bc_nocur"}}
	s.txns["bc_other"] = map[string]any{"id": 79, "success": true, "amount_cents": 100, "order": map[string]any{"merchant_order_id": "someone-else"}}
	if txn, paid, err := g.Paid(t.Context(), "EGP", "bc_paid", 68286); err != nil || !paid || txn != "77" {
		t.Errorf("paid = %s %v %v", txn, paid, err)
	}
	if _, paid, err := g.Paid(t.Context(), "EGP", "bc_short", 68286); err != nil || paid {
		t.Errorf("a partial amount is not paid: %v %v", paid, err)
	}
	if _, paid, err := g.Paid(t.Context(), "EGP", "bc_none", 1); err != nil || paid {
		t.Errorf("no transaction: %v %v", paid, err)
	}
	if _, _, err := g.Paid(t.Context(), "EGP", "bc_other", 100); err == nil {
		t.Error("an answer about another payment believed")
	}
	if _, paid, err := g.Paid(t.Context(), "EGP", "bc_nocur", 68286); err == nil || paid {
		t.Errorf("an answer without its currency believed: %v %v", paid, err)
	}
	if _, _, err := g.Paid(t.Context(), "AED", "bc_paid", 1); err == nil {
		t.Error("a region without an account answered")
	}
}

// FuzzParseCallback: a callback is accepted only with a region's HMAC of
// its fields, and what is read from it is usable: our reference or
// Paymob's order to place it, a card label that fits its column.
func FuzzParseCallback(f *testing.F) {
	srv := httptest.NewServer(http.NotFoundHandler())
	f.Cleanup(srv.Close)
	g := checkoutGateway(srv)
	f.Add([]byte(`{"type":"TRANSACTION","obj":{"id":9001,"success":true,"order":{"id":4547,"merchant_order_id":"bc_01"}}}`), "00")
	f.Add([]byte(`{"type":"TOKEN","obj":{"id":1,"token":"t","masked_pan":"xxxx-2346","card_subtype":"Visa","order_id":"7"}}`), "") // nocarddata:allow a masked card
	f.Add([]byte(`{"type":"TOKEN","obj":{"id":1,"token":"t","masked_pan":"éé","card_subtype":"","order_id":"7"}}`), "")      // nocarddata:allow a masked card
	f.Add([]byte(`{"obj":null}`), "ab")
	f.Fuzz(func(t *testing.T, body []byte, mac string) {
		cb, err := g.ParseCallback(url.Values{"hmac": {mac}}, body)
		if err == nil {
			fields := platformpaymob.TransactionFields()
			if cb.Kind == app.CallbackToken {
				fields = platformpaymob.TokenFields()
			}
			okEG, _ := platformpaymob.Sign("hmac-eg", body, fields)
			okSA, _ := platformpaymob.Sign("hmac-sa", body, fields)
			if !strings.EqualFold(mac, okEG) && !strings.EqualFold(mac, okSA) {
				t.Fatalf("accepted %q with hmac %q", body, mac)
			}
		}
		read, err := g.ReadCallback(body)
		if err != nil {
			if !errors.Is(err, app.ErrBadCallback) {
				t.Fatalf("read %q: %v", body, err)
			}
			return
		}
		switch read.Kind {
		case app.CallbackToken:
			if read.Token == "" || read.ProviderOrderID == "" || !utf8.ValidString(read.Label) || utf8.RuneCountInString(read.Label) > 60 {
				t.Fatalf("token %q read as %+v", body, read)
			}
		case app.CallbackTransaction:
			if read.Reference == "" || read.TransactionID == "" || read.EventID == "" {
				t.Fatalf("transaction %q read as %+v", body, read)
			}
		default:
			t.Fatalf("%q read as kind %v", body, read.Kind)
		}
	})
}
