package capi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
)

// server records what each provider was sent and answers status with
// body.
type server struct {
	*httptest.Server
	mu     sync.Mutex
	got    []*http.Request
	bodies []map[string]any
}

func newServer(t *testing.T, status int, body string) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s.mu.Lock()
		s.got, s.bodies = append(s.got, r), append(s.bodies, m)
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func event() capi.Event {
	return capi.Event{Name: capi.Purchase, ID: "purchase:01M6ORDER", Time: time.Unix(1_761_700_000, 0), ValueMinor: 45050, Units: 2, Currency: "EGP",
		Items: []string{"01M6VARIANT"}, OrderID: "01M6ORDER", URL: "https://mona.iftahonline.com/thank-you/01M6ORDER",
		User: capi.User{Phone: "+201012345678", ExternalID: "01M6CUSTOMER", Country: "EG", IP: "203.0.113.7", UserAgent: "Mozilla/5.0",
			FBP: "fb.1.2.3", FBC: "fb.1.2.click", TTP: "ttp-1", TTCLID: "tt-click", ScCID: "sc-click"}}
}

func first(t *testing.T, s *server) (*http.Request, map[string]any) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.got) != 1 {
		t.Fatalf("%d requests, want 1", len(s.got))
	}
	return s.got[0], s.bodies[0]
}

func TestMeta(t *testing.T) {
	t.Parallel()
	s := newServer(t, http.StatusOK, `{"events_received":1}`)
	err := capi.Meta{Base: s.URL, Client: s.Client()}.Send(t.Context(), capi.Pixel{ID: "123456789012345", Token: "EAAB-token", TestCode: "TEST1"}, event())
	if err != nil {
		t.Fatal(err)
	}
	r, b := first(t, s)
	if r.URL.Path != "/123456789012345/events" || r.URL.RawQuery != "" || b["access_token"] != "EAAB-token" || b["test_event_code"] != "TEST1" {
		t.Fatalf("request %s %v", r.URL, b)
	}
	ev := b["data"].([]any)[0].(map[string]any)
	user, custom := ev["user_data"].(map[string]any), ev["custom_data"].(map[string]any)
	if ev["event_name"] != "Purchase" || ev["event_id"] != "purchase:01M6ORDER" || ev["event_time"] != float64(1_761_700_000) || ev["action_source"] != "website" {
		t.Fatalf("event %v", ev)
	}
	if user["ph"].([]any)[0] != sha("201012345678") || user["external_id"].([]any)[0] != sha("01M6CUSTOMER") || user["country"].([]any)[0] != sha("eg") ||
		user["client_ip_address"] != "203.0.113.7" || user["fbp"] != "fb.1.2.3" || user["fbc"] != "fb.1.2.click" {
		t.Fatalf("user_data %v", user)
	}
	if custom["value"] != 450.5 || custom["currency"] != "EGP" || custom["order_id"] != "01M6ORDER" || custom["content_ids"].([]any)[0] != "01M6VARIANT" {
		t.Fatalf("custom_data %v", custom)
	}
	if strings.Contains(r.URL.String(), "token") {
		t.Fatal("the token is in the URL")
	}
}

func TestTikTok(t *testing.T) {
	t.Parallel()
	s := newServer(t, http.StatusOK, `{"code":0,"message":"OK"}`)
	if err := (capi.TikTok{Base: s.URL, Client: s.Client()}).Send(t.Context(), capi.Pixel{ID: "C4ABCDEF1234567890AB", Token: "tt-token"}, event()); err != nil {
		t.Fatal(err)
	}
	r, b := first(t, s)
	if r.URL.Path != "/event/track/" || r.Header.Get("Access-Token") != "tt-token" || b["event_source_id"] != "C4ABCDEF1234567890AB" || b["event_source"] != "web" {
		t.Fatalf("request %s %v %v", r.URL, r.Header, b)
	}
	ev := b["data"].([]any)[0].(map[string]any)
	user, props := ev["user"].(map[string]any), ev["properties"].(map[string]any)
	if ev["event"] != "CompletePayment" || ev["event_id"] != "purchase:01M6ORDER" || user["phone"] != sha("+201012345678") || user["ttclid"] != "tt-click" ||
		user["ttp"] != "ttp-1" || props["value"] != 450.5 || props["order_id"] != "01M6ORDER" {
		t.Fatalf("event %v", ev)
	}
	// TikTok refuses with 200 and a code.
	bad := newServer(t, http.StatusOK, `{"code":40001,"message":"bad token"}`)
	if err := (capi.TikTok{Base: bad.URL, Client: bad.Client()}).Send(t.Context(), capi.Pixel{ID: "X", Token: "t"}, event()); !errors.Is(err, capi.ErrRejected) {
		t.Fatalf("code 40001: %v", err)
	}
}

func TestSnap(t *testing.T) {
	t.Parallel()
	s := newServer(t, http.StatusOK, `{"status":"VALID"}`)
	if err := (capi.Snap{Base: s.URL, Client: s.Client()}).Send(t.Context(), capi.Pixel{ID: "0f8fad5b-d9cb-469f-a165-70867728950e", Token: "sn-token"}, event()); err != nil {
		t.Fatal(err)
	}
	r, b := first(t, s)
	if r.URL.Path != "/0f8fad5b-d9cb-469f-a165-70867728950e/events" || r.URL.Query().Get("access_token") != "sn-token" {
		t.Fatalf("request %s", r.URL)
	}
	ev := b["data"].([]any)[0].(map[string]any)
	if ev["event_name"] != "PURCHASE" || ev["event_id"] != "purchase:01M6ORDER" || ev["action_source"] != "WEB" ||
		ev["user_data"].(map[string]any)["sc_click_id"] != "sc-click" || ev["user_data"].(map[string]any)["ph"].([]any)[0] != sha("201012345678") {
		t.Fatalf("event %v", ev)
	}
}

func TestGA4(t *testing.T) {
	t.Parallel()
	s := newServer(t, http.StatusNoContent, "")
	if err := (capi.GA4{Base: s.URL, Client: s.Client()}).Send(t.Context(), capi.Pixel{ID: "G-ABC123XYZ", Token: "ga-secret"}, event()); err != nil {
		t.Fatal(err)
	}
	r, b := first(t, s)
	if r.URL.Path != "/mp/collect" || r.URL.Query().Get("measurement_id") != "G-ABC123XYZ" || r.URL.Query().Get("api_secret") != "ga-secret" ||
		b["client_id"] != "iftah.01M6CUSTOMER" {
		t.Fatalf("request %s %v", r.URL, b)
	}
	ev := b["events"].([]any)[0].(map[string]any)
	params := ev["params"].(map[string]any)
	if ev["name"] != "purchase" || params["transaction_id"] != "01M6ORDER" || params["value"] != 450.5 || params["event_id"] != "purchase:01M6ORDER" {
		t.Fatalf("event %v", ev)
	}
	if strings.Contains(string(mustJSON(t, b)), "201012345678") {
		t.Fatal("GA4 got the phone")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAnswers: 4xx (but 408 and 429) is a refusal for good; 5xx, 408,
// 429 and transport errors may be retried; a transport error never
// carries the URL (a token may be in it).
func TestAnswers(t *testing.T) {
	t.Parallel()
	for status, rejected := range map[int]bool{400: true, 401: true, 403: true, 408: false, 429: false, 500: false, 503: false} {
		s := newServer(t, status, "{}")
		err := capi.Snap{Base: s.URL, Client: s.Client()}.Send(t.Context(), capi.Pixel{ID: "p", Token: "secret-token"}, event())
		if err == nil || errors.Is(err, capi.ErrRejected) != rejected || strings.Contains(err.Error(), "secret-token") {
			t.Errorf("status %d: %v (rejected %v)", status, err, rejected)
		}
	}
	err := capi.Snap{Base: "http://127.0.0.1:1", Client: http.DefaultClient}.Send(context.Background(), capi.Pixel{ID: "p", Token: "secret-token"}, event())
	if err == nil || errors.Is(err, capi.ErrRejected) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("transport error %v", err)
	}
}

// TestValue: minor units become the decimal amount, exactly.
func TestValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		minor int64
		units int
		want  float64
	}{{45000, 2, 450}, {5, 2, 0.05}, {1234, 0, 1234}, {1001, 3, 1.001}} {
		s := newServer(t, http.StatusOK, "{}")
		e := event()
		e.ValueMinor, e.Units = tc.minor, tc.units
		if err := (capi.Meta{Base: s.URL, Client: s.Client()}).Send(t.Context(), capi.Pixel{ID: "1", Token: "t"}, e); err != nil {
			t.Fatal(err)
		}
		_, b := first(t, s)
		if v := b["data"].([]any)[0].(map[string]any)["custom_data"].(map[string]any)["value"]; v != tc.want {
			t.Errorf("%d/%d = %v, want %v", tc.minor, tc.units, v, tc.want)
		}
	}
}

// TestNoRedirect: the client never follows a redirect, so a token in the
// URL (Snap, GA4) never reaches another host.
func TestNoRedirect(t *testing.T) {
	t.Parallel()
	elsewhere := newServer(t, http.StatusOK, "{}")
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(moved.Close)
	err := capi.Snap{Base: moved.URL, Client: capi.NewClient()}.Send(t.Context(), capi.Pixel{ID: "p", Token: "secret-token"}, event())
	if err == nil || errors.Is(err, capi.ErrRejected) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("redirect: %v", err)
	}
	elsewhere.mu.Lock()
	defer elsewhere.mu.Unlock()
	if len(elsewhere.got) != 0 {
		t.Fatal("the redirect was followed, with the token")
	}
}
