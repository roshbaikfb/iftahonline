// Package paymobtest is an in-process stand-in for Paymob's API, built
// from the shapes the adapter follows (package paymob): the adapter's
// tests and the contract suite run against it without a network. Pay and
// Decline play the shopper on the hosted page; Callback builds the signed
// transaction callback Paymob would send.
package paymobtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/paymob"
)

// Test credentials the server accepts.
const (
	SecretKey  = "sk_test_secret"
	PublicKey  = "pk_test_public"
	APIKey     = "api_test_key"
	HMACSecret = "hmac_test_secret"
)

// Credentials are the accepted credentials.
func Credentials() gateway.Credentials {
	return gateway.Credentials{SecretKey: SecretKey, PublicKey: PublicKey, WebhookSecret: HMACSecret, APIKey: APIKey, IntegrationIDs: []int{4100}}
}

// Server is the stand-in.
type Server struct {
	URL string

	mu      sync.Mutex
	seq     int64
	byRef   map[string]*txn
	byID    map[int64]*txn
	intents map[string]map[string]any
}

type txn struct {
	id                      int64
	ref                     string
	amount, refunded        int64
	success, pending, voids bool
	paid                    bool
}

// New starts a server closed at the end of the test.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{byRef: map[string]*txn{}, byID: map[int64]*txn{}, intents: map[string]map[string]any{}}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// Intention returns the body an intention was created with.
func (s *Server) Intention(ref string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intents[ref]
}

// Pay pays an intention's amount, as the shopper would.
func (s *Server) Pay(ref string) error { return s.settle(ref, true) }

// Decline declines it.
func (s *Server) Decline(ref string) error { return s.settle(ref, false) }

func (s *Server) settle(ref string, ok bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, found := s.intents[ref]
	if !found {
		return fmt.Errorf("paymobtest: no intention %s", ref)
	}
	s.seq++
	amount, _ := in["amount"].(float64) //nolint:errcheck // validated at creation
	t := &txn{id: 900000 + s.seq, ref: ref, amount: int64(amount), success: ok, paid: ok}
	s.byRef[ref], s.byID[t.id] = t, t
	return nil
}

// Callback is the signed transaction callback for an intention's
// transaction: the body and the query carrying hmac.
func (s *Server) Callback(ref string) ([]byte, url.Values, error) {
	s.mu.Lock()
	t, ok := s.byRef[ref]
	var obj map[string]any
	if ok {
		obj = s.obj(t)
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("paymobtest: no transaction for %s", ref)
	}
	body, err := json.Marshal(map[string]any{"type": "TRANSACTION", "obj": obj})
	if err != nil {
		return nil, nil, err
	}
	h, err := paymob.Sign(HMACSecret, body)
	if err != nil {
		return nil, nil, err
	}
	return body, url.Values{"hmac": {h}}, nil
}

func (s *Server) obj(t *txn) map[string]any {
	return map[string]any{
		"id": t.id, "pending": t.pending, "amount_cents": t.amount, "success": t.success, "is_auth": false, "is_capture": false,
		"is_standalone_payment": true, "is_voided": t.voids, "is_refunded": t.refunded > 0, "is_3d_secure": true,
		"integration_id": 4100, "has_parent_transaction": false, "currency": "EGP", "error_occured": !t.success,
		"owner": 1234, "created_at": "2026-10-23T12:00:00.000000", "refunded_amount_cents": t.refunded,
		"order":       map[string]any{"id": 5000 + t.id, "merchant_order_id": t.ref},
		"source_data": map[string]any{"type": "card", "sub_type": "MasterCard", "pan": "2346"}, // nocarddata:allow masked last four digits, as Paymob sends them
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var in map[string]any
	if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &in) //nolint:errcheck // a bad body fails the checks below
	}
	auth := r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/tokens":
		if in["api_key"] != APIKey {
			reply(w, http.StatusForbidden, map[string]any{"detail": "Incorrect credentials"})
			return
		}
		reply(w, http.StatusCreated, map[string]any{"token": "tok_" + APIKey})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/intention/":
		if auth != "Token "+SecretKey {
			reply(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid token"})
			return
		}
		s.intention(w, in)
	case r.Method == http.MethodPost && r.URL.Path == "/api/ecommerce/orders/transaction_inquiry":
		if auth != "Bearer tok_"+APIKey {
			reply(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid token"})
			return
		}
		ref, _ := in["merchant_order_id"].(string) //nolint:errcheck // a missing ref finds nothing
		t, ok := s.byRef[ref]
		if !ok {
			reply(w, http.StatusNotFound, map[string]any{"detail": "Not found."})
			return
		}
		reply(w, http.StatusOK, s.obj(t))
	case r.Method == http.MethodPost && r.URL.Path == "/api/acceptance/void_refund/refund":
		if auth != "Token "+SecretKey {
			reply(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid token"})
			return
		}
		id, _ := in["transaction_id"].(float64)   //nolint:errcheck // validated below
		amount, _ := in["amount_cents"].(float64) //nolint:errcheck // validated below
		t, ok := s.byID[int64(id)]
		switch {
		case !ok:
			reply(w, http.StatusNotFound, map[string]any{"detail": "Not found."})
		case !t.paid || amount <= 0 || int64(amount) > t.amount-t.refunded:
			reply(w, http.StatusBadRequest, map[string]any{"message": "Refund amount exceeds the captured amount"})
		default:
			t.refunded += int64(amount)
			s.seq++
			reply(w, http.StatusOK, map[string]any{"id": 900000 + s.seq, "success": true, "amount_cents": int64(amount)})
		}
	default:
		reply(w, http.StatusNotFound, map[string]any{"detail": "Not found."})
	}
}

func (s *Server) intention(w http.ResponseWriter, in map[string]any) {
	ref, _ := in["special_reference"].(string)        //nolint:errcheck // checked below
	amount, _ := in["amount"].(float64)               //nolint:errcheck // checked below
	items, _ := in["items"].([]any)                   //nolint:errcheck // checked below
	billing, _ := in["billing_data"].(map[string]any) //nolint:errcheck // a missing billing fails the check below
	var sum float64
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			a, _ := m["amount"].(float64)   //nolint:errcheck // summed
			q, _ := m["quantity"].(float64) //nolint:errcheck // summed
			sum += a * q
		}
	}
	switch {
	case ref == "" || amount <= 0 || sum != amount || billing["phone_number"] == "" || billing["email"] == "" || billing["first_name"] == "":
		reply(w, http.StatusBadRequest, map[string]any{"detail": "amount must equal the items' sum; billing data is required"})
		return
	case s.intents[ref] != nil:
		reply(w, http.StatusBadRequest, map[string]any{"special_reference": []string{"special reference already exists"}})
		return
	}
	s.intents[ref] = in
	s.seq++
	reply(w, http.StatusCreated, map[string]any{"id": "pi_test_" + strconv.FormatInt(s.seq, 10), "client_secret": "csk_" + ref,
		"intention_order_id": 5000 + s.seq, "created": time.Now().UTC().Format(time.RFC3339)})
}

func reply(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body) //nolint:errcheck // test server
}
