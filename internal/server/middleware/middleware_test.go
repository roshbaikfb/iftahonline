package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

func newReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), method, target, body)
}

func logger(buf *bytes.Buffer) *slog.Logger {
	return logging.New(logging.Options{Writer: buf, Level: slog.LevelDebug})
}

func TestChainOrder(t *testing.T) {
	t.Parallel()
	var order []string
	mk := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := middleware.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "h") }), mk("a"), mk("b"))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	if strings.Join(order, ",") != "a,b,h" {
		t.Fatalf("order = %v", order)
	}
}

func TestRequestID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, inbound string
		keep          bool
	}{
		{"generated", "", false},
		{"accepted", "edge-abc_123.4", true},
		{"too long", strings.Repeat("a", 65), false},
		{"injection", "x\r\nSet-Cookie: a=b", false},
		{"bad chars", "a b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var seen string
			h := middleware.RequestID(ulid.NewGenerator())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = logging.RequestID(r.Context())
			}))
			req := newReq(t, http.MethodGet, "/", nil)
			if tt.inbound != "" {
				req.Header.Set(middleware.RequestIDHeader, tt.inbound)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			got := rec.Header().Get(middleware.RequestIDHeader)
			if got != seen || got == "" {
				t.Fatalf("header %q, ctx %q", got, seen)
			}
			if tt.keep != (got == tt.inbound) {
				t.Fatalf("got %q for inbound %q", got, tt.inbound)
			}
			if !tt.keep {
				if _, err := ulid.Parse(got); err != nil {
					t.Fatalf("generated id %q is not a ULID", got)
				}
			}
		})
	}
}

func TestRecover(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := logger(&logs)
	h := middleware.Recover(log, problem.NewResponder(log))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		items := []int{1}
		_ = items[len(r.URL.Path)] // index out of range: a real bug
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq(t, http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != problem.ContentType {
		t.Fatalf("status %d, ct %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Body.String(), "index out of range") {
		t.Fatalf("panic detail leaked: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "panic recovered") || !strings.Contains(logs.String(), "stack") {
		t.Fatalf("panic not logged: %s", logs.String())
	}
}

func TestRecoverAbortHandler(t *testing.T) {
	t.Parallel()
	log := logger(&bytes.Buffer{})
	h := middleware.Recover(log, problem.NewResponder(log))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler { //nolint:errorlint // identity check.
			t.Fatalf("recovered %v, want ErrAbortHandler re-panic", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
}

func TestAccessLog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		status int
		level  string
	}{
		{"/v1/x?token=secret", http.StatusCreated, "INFO"},
		{"/v1/fail", http.StatusBadGateway, "ERROR"},
		{"/livez", 0, "DEBUG"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			h := middleware.AccessLog(logger(&logs))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = io.WriteString(w, "hello")
			}))
			h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodPost, tt.path, nil))
			out := logs.String()
			if !strings.Contains(out, `"level":"`+tt.level+`"`) || !strings.Contains(out, `"bytes":5`) {
				t.Fatalf("log = %s", out)
			}
			if strings.Contains(out, "secret") {
				t.Fatalf("query string leaked: %s", out)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		p        middleware.Profile
		csp      string
		referrer string
	}{
		{middleware.ProfileAPI, "default-src 'none'", "no-referrer"},
		{middleware.ProfileStorefront, "script-src 'self';", "strict-origin-when-cross-origin"},
	} {
		rec := httptest.NewRecorder()
		middleware.SecurityHeaders(tt.p)(http.NotFoundHandler()).ServeHTTP(rec, newReq(t, http.MethodGet, "/", nil))
		h := rec.Header()
		if !strings.Contains(h.Get("Content-Security-Policy"), tt.csp) || strings.Contains(h.Get("Content-Security-Policy"), "unsafe") ||
			h.Get("Referrer-Policy") != tt.referrer || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("X-Frame-Options") != "DENY" || !strings.HasPrefix(h.Get("Strict-Transport-Security"), "max-age=") ||
			h.Get("Cross-Origin-Opener-Policy") != "same-origin" || h.Get("Permissions-Policy") == "" {
			t.Errorf("profile %d headers = %v", tt.p, h)
		}
	}
}

func TestMaxBody(t *testing.T) {
	t.Parallel()
	log := logger(&bytes.Buffer{})
	resp := problem.NewResponder(log)
	h := middleware.MaxBody(8, resp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			resp.Write(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tt := range []struct {
		name    string
		body    io.Reader
		chunked bool
		want    int
	}{
		{"small", strings.NewReader("1234"), false, http.StatusNoContent},
		{"declared too large", strings.NewReader("123456789"), false, http.StatusRequestEntityTooLarge},
		{"streamed too large", strings.NewReader("123456789"), true, http.StatusRequestEntityTooLarge},
	} {
		req := newReq(t, http.MethodPost, "/", tt.body)
		if tt.chunked {
			req.ContentLength = -1
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	var deadline time.Time
	h := middleware.Timeout(50 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, _ = r.Context().Deadline()
		<-r.Context().Done()
		if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			t.Errorf("ctx err = %v", r.Context().Err())
		}
	}))
	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	if deadline.IsZero() || time.Since(start) > 2*time.Second {
		t.Fatalf("deadline %v not applied", deadline)
	}
}

func TestRealIP(t *testing.T) {
	t.Parallel()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct, xff ignored", "203.0.113.9:5000", []string{"1.2.3.4"}, "203.0.113.9"},
		{"trusted proxy", "10.0.0.5:5000", []string{"1.2.3.4"}, "1.2.3.4"},
		{"spoofed left-most ignored", "10.0.0.5:5000", []string{"6.6.6.6, 1.2.3.4"}, "1.2.3.4"},
		{"proxy chain", "10.0.0.5:5000", []string{"1.2.3.4, 10.0.0.7"}, "1.2.3.4"},
		{"multiple headers", "10.0.0.5:5000", []string{"6.6.6.6", "1.2.3.4"}, "1.2.3.4"},
		{"garbage hop stops", "10.0.0.5:5000", []string{"1.2.3.4, junk"}, "10.0.0.5"},
		{"ipv6 peer", "[2001:db8::1]:443", nil, "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			var logs bytes.Buffer
			h := middleware.RealIP(trusted)(middleware.AccessLog(logger(&logs))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ip, _ := middleware.ClientIP(r.Context())
				got = ip.String()
			})))
			req := newReq(t, http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want || !strings.Contains(logs.String(), `"client_ip":"`+tt.want+`"`) {
				t.Fatalf("got %q, want %q; log %s", got, tt.want, logs.String())
			}
		})
	}
	// Unparsable peer: no client IP in context, nothing logged.
	var logs bytes.Buffer
	h := middleware.RealIP(trusted)(middleware.AccessLog(logger(&logs))(http.NotFoundHandler()))
	req := newReq(t, http.MethodGet, "/", nil)
	req.RemoteAddr = "@unix"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(logs.String(), `"client_ip":""`) {
		t.Fatalf("log = %s", logs.String())
	}
}
