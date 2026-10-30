package paymob_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/gateway/paymob"
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
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_1", "payment_keys": []map[string]any{{"key": key, "integration": 777}}})
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
