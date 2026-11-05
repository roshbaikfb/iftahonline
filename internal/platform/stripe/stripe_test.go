package stripe_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/stripe"
)

// permanent reports whether retry.Do gives up on err at once.
func permanent(t *testing.T, err error) bool {
	t.Helper()
	calls := 0
	_ = retry.Do(t.Context(), retry.Policy{MaxAttempts: 3, Initial: time.Millisecond, Max: time.Millisecond}, func(context.Context) error {
		calls++
		return err
	})
	return calls == 1
}

// TestDo: the request Stripe expects (key, pinned version, form or
// query, idempotency key) and its answers mapped: a decline is a
// *CardError, 404 no error, 409/429/5xx retryable, other 4xx permanent.
func TestDo(t *testing.T) {
	t.Parallel()
	var got *http.Request
	var body string
	status, answer := http.StatusOK, `{"id":"pi_1"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	c := stripe.New(stripe.Options{SecretKey: "rk_test_1", BaseURL: srv.URL + "/", Client: srv.Client()})

	var out struct {
		ID string `json:"id"`
	}
	if _, err := c.Do(t.Context(), http.MethodPost, "/v1/payment_intents", url.Values{"amount": {"100"}}, "charge:INV-1", &out); err != nil || out.ID != "pi_1" {
		t.Fatalf("post = %+v, %v", out, err)
	}
	if got.Header.Get("Authorization") != "Bearer rk_test_1" || got.Header.Get("Stripe-Version") != stripe.APIVersion ||
		got.Header.Get("Idempotency-Key") != "charge:INV-1" || got.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || body != "amount=100" {
		t.Fatalf("request = %v %q", got.Header, body)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, "/v1/checkout/sessions/cs_1", url.Values{"expand[]": {"payment_intent"}}, "", nil); err != nil ||
		got.URL.Query().Get("expand[]") != "payment_intent" || body != "" || got.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("get = %v, %q, %v", got.URL, body, err)
	}

	status, answer = http.StatusPaymentRequired, `{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","message":"Your card has insufficient funds.","payment_intent":{"id":"pi_2"}}}`
	_, err := c.Do(t.Context(), http.MethodPost, "/v1/payment_intents", nil, "k", nil)
	var declined *stripe.CardError
	if !errors.As(err, &declined) || declined.Reason() != "Your card has insufficient funds." || !strings.Contains(string(declined.PaymentIntent), "pi_2") ||
		!strings.Contains(declined.Error(), "insufficient_funds") {
		t.Fatalf("decline = %v", err)
	}
	for _, s := range []int{http.StatusConflict, http.StatusTooManyRequests, http.StatusBadGateway} {
		status, answer = s, `{}`
		if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); err == nil || permanent(t, err) {
			t.Errorf("%d: %v", s, err)
		}
	}
	status, answer = http.StatusNotFound, `{"error":{"message":"No such checkout.session"}}`
	if code, nfErr := c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); nfErr != nil || code != http.StatusNotFound {
		t.Errorf("404 = %d, %v", code, nfErr)
	}
	status, answer = http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"No such customer: 'cus_x'"}}`
	if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); !errors.Is(err, stripe.ErrRejected) || !permanent(t, err) ||
		!strings.Contains(err.Error(), "invalid_request_error") {
		t.Errorf("400 = %v", err)
	}
	status, answer = http.StatusPaymentRequired, `not json`
	if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); !errors.Is(err, stripe.ErrRejected) {
		t.Errorf("unreadable decline = %v", err)
	}
	status, answer = http.StatusUnauthorized, `{}`
	if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); !errors.Is(err, stripe.ErrAuth) || !permanent(t, err) {
		t.Errorf("401 = %v", err)
	}
	status, answer = http.StatusOK, `not json`
	if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", &out); err == nil || !permanent(t, err) {
		t.Errorf("an unreadable answer = %v", err)
	}
	if _, err = stripe.New(stripe.Options{}).Do(t.Context(), http.MethodGet, "/x", nil, "", nil); !errors.Is(err, stripe.ErrAuth) {
		t.Errorf("no key = %v", err)
	}
	srv.Close()
	if _, err = c.Do(t.Context(), http.MethodGet, "/x", nil, "", nil); err == nil || permanent(t, err) {
		t.Errorf("a network error = %v", err)
	}
}

func TestCardErrorReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		e    stripe.CardError
		want string
	}{
		{stripe.CardError{Message: " Declined. ", Code: "card_declined"}, "Declined."},
		{stripe.CardError{DeclineCode: "do_not_honor", Code: "card_declined"}, "do_not_honor"},
		{stripe.CardError{Code: "expired_card"}, "expired_card"},
		{stripe.CardError{}, "declined"},
	} {
		if got := tc.e.Reason(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.e, got, tc.want)
		}
	}
}

// TestVerify: Stripe's signature over "<t>.<body>", one of several v1
// values, recent; anything else is refused.
func TestVerify(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	good := stripe.Sign("whsec_1", body, now)
	other := stripe.Sign("whsec_old", body, now)
	rolled := other + "," + strings.Split(good, ",")[1] // two v1 values while the secret rolls
	for name, tc := range map[string]struct {
		secret, header string
		body           []byte
		at             time.Time
		ok             bool
	}{
		"signed":              {"whsec_1", good, body, now, true},
		"a minute later":      {"whsec_1", good, body, now.Add(time.Minute), true},
		"rolled":              {"whsec_1", rolled, body, now, true},
		"another secret":      {"whsec_1", other, body, now, false},
		"another body":        {"whsec_1", good, []byte(`{"id":"evt_2"}`), now, false},
		"replayed":            {"whsec_1", good, body, now.Add(stripe.Tolerance + time.Second), false},
		"from the future":     {"whsec_1", good, body, now.Add(-stripe.Tolerance - time.Second), false},
		"no secret":           {"", good, body, now, false},
		"no header":           {"whsec_1", "", body, now, false},
		"v0 only":             {"whsec_1", strings.Replace(good, "v1=", "v0=", 1), body, now, false},
		"two timestamps":      {"whsec_1", good + ",t=1", body, now, false},
		"a timestamp of zero": {"whsec_1", "t=0," + strings.Split(good, ",")[1], body, now, false},
		"not hex":             {"whsec_1", "t=1800000000,v1=zz", body, now, false},
		"an oversized header": {"whsec_1", good + "," + strings.Repeat("x", 5000), body, now, false},
	} {
		if err := stripe.Verify(tc.secret, tc.header, tc.body, tc.at); (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzParseSignature: any header is read without panicking; what it
// accepts has a timestamp and 32-byte signatures.
func FuzzParseSignature(f *testing.F) {
	f.Add(stripe.Sign("whsec_1", []byte("{}"), time.Unix(1_800_000_000, 0)))
	f.Add("t=1,v1=00,v0=11")
	f.Add(",,=,t=")
	f.Fuzz(func(t *testing.T, header string) {
		ts, sigs, ok := stripe.ParseSignature(header)
		if !ok {
			return
		}
		if ts <= 0 || len(sigs) == 0 || len(sigs) > 8 {
			t.Fatalf("accepted %q: %d %d", header, ts, len(sigs))
		}
		for _, s := range sigs {
			if len(s) != 32 {
				t.Fatalf("a %d-byte signature from %q", len(s), header)
			}
		}
	})
}

func TestParseEvent(t *testing.T) {
	t.Parallel()
	e, err := stripe.ParseEvent([]byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1"}}}`))
	if err != nil || e.ID != "evt_1" || e.Type != "checkout.session.completed" || !strings.Contains(string(e.Data.Object), "cs_1") {
		t.Fatalf("event = %+v, %v", e, err)
	}
	for _, bad := range []string{`x`, `{}`, `{"id":"1","type":"a","data":{"object":{}}}`, `{"id":"evt_1","data":{"object":{}}}`, `{"id":"evt_1","type":"a"}`} {
		if _, err = stripe.ParseEvent([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if stripe.Currency("USD") != "usd" {
		t.Error("currency")
	}
}

// FuzzParseEvent: any body is read without panicking; an event it
// accepts has an ID, a type and an object.
func FuzzParseEvent(f *testing.F) {
	f.Add([]byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1"}}}`))
	f.Add([]byte(`{"id":"evt_","type":"","data":null}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		e, err := stripe.ParseEvent(body)
		if err == nil && (!strings.HasPrefix(e.ID, "evt_") || e.Type == "" || len(e.Data.Object) == 0) {
			t.Fatalf("accepted %q", body)
		}
	})
}
