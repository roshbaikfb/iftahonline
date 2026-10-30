package paymob_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/paymob"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
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

func TestBaseFor(t *testing.T) {
	t.Parallel()
	c := paymob.New(paymob.Options{})
	for cur, want := range map[string]string{"EGP": paymob.EgyptURL, "SAR": paymob.SaudiURL, "AED": paymob.UAEURL} {
		if got, err := c.BaseFor(cur); err != nil || got != want {
			t.Errorf("%s: %q %v", cur, got, err)
		}
	}
	if _, err := c.BaseFor("USD"); !errors.Is(err, paymob.ErrRejected) || !permanent(t, err) {
		t.Errorf("USD: %v", err)
	}
	if got, _ := paymob.New(paymob.Options{BaseURL: "http://stub/"}).BaseFor("SAR"); got != "http://stub" {
		t.Errorf("override: %q", got)
	}
}

func TestDo(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token sk" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"id":"pi_1"}`))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/busy":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"amount must be positive"}`))
		case "/garbage":
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	t.Cleanup(srv.Close)
	c := paymob.New(paymob.Options{BaseURL: srv.URL})
	var out struct {
		ID string `json:"id"`
	}
	if code, err := c.Do(t.Context(), http.MethodPost, srv.URL+"/ok", "Token sk", map[string]int{"amount": 1}, &out); err != nil || code != 200 || out.ID != "pi_1" {
		t.Fatalf("ok: %d %v %+v", code, err, out)
	}
	if code, err := c.Do(t.Context(), http.MethodGet, srv.URL+"/missing", "Token sk", nil, nil); err != nil || code != 404 {
		t.Errorf("missing: %d %v", code, err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, srv.URL+"/busy", "Token sk", nil, nil); err == nil || permanent(t, err) {
		t.Errorf("busy is retryable: %v", err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, srv.URL+"/bad", "Token sk", nil, nil); !errors.Is(err, paymob.ErrRejected) || !permanent(t, err) || !strings.Contains(err.Error(), "positive") {
		t.Errorf("bad: %v", err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, srv.URL+"/ok", "Token wrong", nil, nil); !errors.Is(err, paymob.ErrAuth) {
		t.Errorf("auth: %v", err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, srv.URL+"/garbage", "Token sk", nil, &out); !permanent(t, err) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, "http://127.0.0.1:1/x", "", nil, nil); err == nil {
		t.Error("unreachable: want an error")
	}
}

// TestTokenHMAC checks the card token callback's signature over Paymob's
// eight sorted fields.
func TestTokenHMAC(t *testing.T) {
	t.Parallel()
	body := []byte(`{"type":"TOKEN","obj":{"id":12929738,"token":"fb9fb86d94ff","masked_pan":"xxxx-xxxx-xxxx-2346","merchant_id":1058607,` + // nocarddata:allow Paymob's masked card in its documented sample
		`"card_subtype":"MasterCard","created_at":"2026-01-20T14:16:05.038717","email":"m@example.com","order_id":"454732315","user_added":false}}`)
	cb, err := paymob.ParseCallback(body)
	if err != nil || cb.Type != "TOKEN" {
		t.Fatal(cb, err)
	}
	want := "MasterCard2026-01-20T14:16:05.038717m@example.com12929738xxxx-xxxx-xxxx-23461058607454732315fb9fb86d94ff"
	if got := paymob.SignedString(cb.Obj, paymob.TokenFields()); got != want {
		t.Fatalf("signed string = %q", got)
	}
	mac, err := paymob.Sign("secret", body, paymob.TokenFields())
	if err != nil {
		t.Fatal(err)
	}
	if !paymob.Verify("secret", mac, cb.Obj, paymob.TokenFields()) || !paymob.Verify("secret", strings.ToUpper(mac), cb.Obj, paymob.TokenFields()) {
		t.Fatal("a good signature is refused")
	}
	for name, tt := range map[string]struct{ secret, mac string }{
		"other secret": {"other", mac}, "no secret": {"", mac}, "not hex": {"secret", "zz"}, "short": {"secret", mac[:64]},
	} {
		if paymob.Verify(tt.secret, tt.mac, cb.Obj, paymob.TokenFields()) {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err = paymob.ParseCallback([]byte(`{"type":"TOKEN"}`)); err == nil {
		t.Error("a callback without obj parsed")
	}
	if _, err = paymob.Sign("s", []byte(`nope`), paymob.TokenFields()); err == nil {
		t.Error("Sign of garbage")
	}
	if n := len(paymob.TransactionFields()); n != 20 {
		t.Errorf("%d transaction fields, want 20", n)
	}
}
