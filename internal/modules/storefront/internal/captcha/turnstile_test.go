package captcha_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/captcha"
)

func TestTurnstile(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var keys []string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("secret") != "s3cret" {
			t.Errorf("form %v %v", r.PostForm, err)
		}
		mu.Lock()
		calls++
		n := calls
		keys = append(keys, r.PostForm.Get("idempotency_key"))
		mu.Unlock()
		switch r.PostForm.Get("response") {
		case "flaky":
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		case "good":
			if r.PostForm.Get("remoteip") != "203.0.113.9" {
				t.Errorf("remoteip %q", r.PostForm.Get("remoteip"))
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		case "down":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "garbage":
			_, _ = w.Write([]byte(`<html>`))
		default:
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
		}
	}))
	t.Cleanup(srv.Close)
	v := captcha.NewTurnstile("s3cret").WithURL(srv.URL)
	ctx := context.Background()

	if ok, err := v.Verify(ctx, "good", "203.0.113.9"); !ok || err != nil {
		t.Fatalf("good: %v %v", ok, err)
	}
	if ok, err := v.Verify(ctx, "bad", ""); ok || err != nil {
		t.Fatalf("bad: %v %v", ok, err)
	}
	if ok, err := v.Verify(ctx, "", ""); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	mu.Lock()
	calls, keys = 0, nil
	mu.Unlock()
	if ok, err := v.Verify(ctx, "flaky", ""); !ok || err != nil {
		t.Fatalf("flaky: %v %v", ok, err)
	}
	mu.Lock()
	if len(keys) != 2 || keys[0] != keys[1] || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(keys[0]) {
		t.Fatalf("a retry must resend the same idempotency key: %v", keys)
	}
	mu.Unlock()
	if _, err := v.Verify(ctx, "down", ""); !errors.Is(err, captcha.ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
	if _, err := v.Verify(ctx, "garbage", ""); !errors.Is(err, captcha.ErrUnavailable) {
		t.Fatalf("garbage: %v", err)
	}
}

// FuzzParseResponse: only {"success": true} passes; nothing panics.
func FuzzParseResponse(f *testing.F) {
	for _, s := range []string{`{"success":true}`, `{"success":false}`, `{"success":"true"}`, `[]`, ``, `{"success":1}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		ok, err := captcha.ParseResponse(b)
		if ok && err != nil {
			t.Fatal("ok with an error")
		}
	})
}
