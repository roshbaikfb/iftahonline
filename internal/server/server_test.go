package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/otel"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

func httpConfig() config.HTTP {
	return config.HTTP{
		Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: time.Minute, RequestTimeout: 2 * time.Second,
		MaxBodyBytes: 1 << 10, ShutdownTimeout: 5 * time.Second, DrainDelay: 200 * time.Millisecond,
	}
}

type fixture struct {
	handler http.Handler
	health  *server.Health
	logs    *bytes.Buffer
}

func newFixture(t *testing.T, profile middleware.Profile, mount func(server.Routes), checks ...server.Check) fixture {
	t.Helper()
	logs := &bytes.Buffer{}
	log := logging.New(logging.Options{Writer: logs, Level: slog.LevelDebug})
	tel, err := otel.Setup(t.Context(), otel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := server.NewHealth(log, server.BuildInfo{Version: "v1.2.3", Commit: "abc1234", Role: "api"}, checks...)
	return fixture{
		handler: server.NewHandler(server.Deps{
			Log: log, IDs: ulid.NewGenerator(), Telemetry: tel, HTTP: httpConfig(), Health: h, Profile: profile, Mount: mount,
		}),
		health: h,
		logs:   logs,
	}
}

func (f fixture) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func ok(context.Context) error { return nil }

func TestLivez(t *testing.T) {
	t.Parallel()
	f := newFixture(t, middleware.ProfileAPI, nil, server.Check{Name: "db", Fn: func(context.Context) error { return errors.New("down") }})
	rec := f.do(t, http.MethodGet, "/livez")
	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "ok" {
		t.Fatalf("livez = %d %s (must not depend on checks)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(middleware.RequestIDHeader) == "" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("middleware chain not applied: %v", rec.Header())
	}
}

func TestReadyz(t *testing.T) {
	t.Parallel()
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	tests := []struct {
		name   string
		checks []server.Check
		drain  bool
		code   int
		status string
		states map[string]string
	}{
		{"all ok", []server.Check{{"postgres", ok}, {"redis", ok}}, false, 200, "ok", map[string]string{"postgres": "ok", "redis": "ok"}},
		{"one failing", []server.Check{{"postgres", ok}, {"redis", func(context.Context) error { return errors.New("refused") }}}, false, 503, "unavailable", map[string]string{"postgres": "ok", "redis": "fail"}},
		{"timeout", []server.Check{{"postgres", slow}}, false, 503, "unavailable", map[string]string{"postgres": "timeout"}},
		{"draining", []server.Check{{"postgres", ok}}, true, 503, "draining", nil},
		{"no checks", nil, false, 200, "ok", map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, middleware.ProfileAPI, nil, tt.checks...)
			if tt.drain {
				f.health.SetDraining()
			}
			rec := f.do(t, http.MethodGet, "/readyz")
			body := decode(t, rec)
			if rec.Code != tt.code || body["status"] != tt.status {
				t.Fatalf("readyz = %d %v", rec.Code, body)
			}
			checks, _ := body["checks"].(map[string]any)
			for k, v := range tt.states {
				if checks[k] != v {
					t.Errorf("check %s = %v, want %s", k, checks[k], v)
				}
			}
			if strings.Contains(rec.Body.String(), "refused") {
				t.Errorf("check error leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	rec := newFixture(t, middleware.ProfileAPI, nil).do(t, http.MethodGet, "/v1/meta/version")
	b := decode(t, rec)
	if rec.Code != 200 || b["version"] != "v1.2.3" || b["commit"] != "abc1234" || b["role"] != "api" || !strings.HasPrefix(b["go_version"].(string), "go") {
		t.Fatalf("version = %d %v", rec.Code, b)
	}
}

func TestUnknownRouteIsProblem(t *testing.T) {
	t.Parallel()
	f := newFixture(t, middleware.ProfileAPI, nil)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		rec := f.do(t, m, "/v1/nope")
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != problem.ContentType {
			t.Fatalf("%s: %d %q", m, rec.Code, rec.Header().Get("Content-Type"))
		}
		if decode(t, rec)["request_id"] != rec.Header().Get(middleware.RequestIDHeader) {
			t.Fatal("problem request_id must match header")
		}
	}
	// Wrong method on a known path also gets a problem body (the catch-all
	// matches every method), never net/http's plain-text error.
	if rec := f.do(t, http.MethodPost, "/livez"); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != problem.ContentType {
		t.Fatalf("POST /livez = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestStorefrontNotFoundPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, middleware.ProfileStorefront, server.MountStorefront(fakeResolver{err: tenancy.ErrUnknownHost}))
	rec := f.do(t, http.MethodGet, "/products/abc")
	body := rec.Body.String()
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, `<html lang="ar" dir="rtl">`) || !strings.Contains(body, "Store not found") ||
		strings.Contains(body, "<script") || strings.Contains(body, "style=") {
		t.Fatalf("storefront = %d %q", rec.Code, body)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
	// Probes still work on the storefront.
	if rec := f.do(t, http.MethodGet, "/livez"); rec.Code != http.StatusOK {
		t.Fatalf("storefront livez = %d", rec.Code)
	}
	// Non-GET falls through to the problem 404.
	if rec := f.do(t, http.MethodPost, "/cart"); rec.Header().Get("Content-Type") != problem.ContentType {
		t.Fatalf("POST = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestRequestTimeoutReachesHandlers(t *testing.T) {
	t.Parallel()
	f := newFixture(t, middleware.ProfileAPI, func(rt server.Routes) {
		rt.Mux.HandleFunc("GET /v1/slow", func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			rt.Problems.Write(w, r, r.Context().Err())
		})
	})
	rec := f.do(t, http.MethodGet, "/v1/slow")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("slow = %d %v", rec.Code, rec.Header())
	}
}

// TestServeGracefulShutdown: on cancel, /readyz flips to 503 during the
// drain delay, an in-flight request completes, and Serve returns nil.
func TestServeGracefulShutdown(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	f := newFixture(t, middleware.ProfileAPI, func(rt server.Routes) {
		rt.Mux.HandleFunc("GET /v1/inflight", func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "done")
		})
	})
	cfg := httpConfig()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	srv := server.NewServer(cfg, f.handler, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, srv, ln, f.health, cfg, slog.New(slog.DiscardHandler)) }()

	get := func(path string) (int, string, error) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			return 0, "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), err
	}
	if code, _, err := get("/readyz"); err != nil || code != http.StatusOK {
		t.Fatalf("readyz before = %d, %v", code, err)
	}

	inflight := make(chan string, 1)
	go func() {
		_, body, err := get("/v1/inflight")
		if err != nil {
			body = err.Error()
		}
		inflight <- body
	}()
	<-started

	cancel() // SIGTERM
	deadline := time.Now().Add(time.Second)
	for !f.health.Draining() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if code, body, err := get("/readyz"); err != nil || code != http.StatusServiceUnavailable || !strings.Contains(body, "draining") {
		t.Fatalf("readyz during drain = %d %q, %v", code, body, err)
	}

	close(release)
	if body := <-inflight; body != "done" {
		t.Fatalf("in-flight request = %q", body)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if _, _, err := get("/livez"); err == nil {
		t.Fatal("server still accepting after shutdown")
	}
}

func TestServeListenerError(t *testing.T) {
	t.Parallel()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // Serve on a closed listener fails immediately
	f := newFixture(t, middleware.ProfileAPI, nil)
	err = server.Serve(t.Context(), server.NewServer(httpConfig(), f.handler, slog.New(slog.DiscardHandler)), ln, f.health, httpConfig(), slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("expected error")
	}
}
