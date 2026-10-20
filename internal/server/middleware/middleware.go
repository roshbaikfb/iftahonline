// Package middleware holds the HTTP middleware shared by every role's
// server. Order matters; see server.NewHandler.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies mws so that the first one is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestIDHeader carries the request ID in and out.
const RequestIDHeader = "X-Request-ID"

// RequestID accepts a well-formed inbound X-Request-ID (from our edge or
// load balancer) or generates a ULID, stores it in the context for logs
// and problems, and echoes it on the response.
func RequestID(gen *ulid.Generator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = gen.New().String()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
		})
	}
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// Recover turns a panic into a logged 500 problem response. Panics are
// bugs (CLAUDE.md rule 10); this keeps one bug from killing the process.
func Recover(log *slog.Logger, resp *problem.Responder) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does.
					panic(rec) //nolint:forbidigo // net/http's contract: re-panic to abort the response.
				}
				log.ErrorContext(r.Context(), "panic recovered", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				resp.Write(w, r, fmt.Errorf("panic: %v", rec))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog logs one line per request: method, path (never the query
// string, which may carry tokens), status, bytes and duration. Probe
// requests are logged at debug level to keep logs useful.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case r.URL.Path == "/livez" || r.URL.Path == "/readyz":
				level = slog.LevelDebug
			}
			log.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("client_ip", clientIP(r)),
			)
		})
	}
}

// Profile selects the security header set.
type Profile int

// Header profiles.
const (
	// ProfileAPI is for JSON APIs: nothing may render or frame responses.
	ProfileAPI Profile = iota
	// ProfileStorefront is for server-rendered HTML. Pages are edge-cached,
	// so per-request CSP nonces are not usable: scripts come only from our
	// origin; a page may add hashes of its inline styles (StorefrontCSP).
	ProfileStorefront
)

// SecurityHeaders sets strict defaults (CLAUDE.md security rules).
func SecurityHeaders(p Profile) Middleware {
	policy := csp.API
	referrer := "no-referrer"
	if p == ProfileStorefront {
		policy = csp.Storefront()
		referrer = "strict-origin-when-cross-origin"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", policy)
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", referrer)
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), usb=(), interest-cohort=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody caps request bodies; reads past the limit fail with
// *http.MaxBytesError, which problem maps to 413.
func MaxBody(n int64, resp *problem.Responder) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				resp.Write(w, r, &http.MaxBytesError{Limit: n})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds the request context. Handlers and every I/O call below
// them must honour ctx (CLAUDE.md rule 8). Unlike http.TimeoutHandler it
// does not buffer responses.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type clientIPKey struct{}

// RealIP resolves the client address. X-Forwarded-For is honoured only
// when the direct peer is a trusted proxy, and then the right-most
// untrusted hop is used (left-most entries are client-controlled).
func RealIP(trusted []netip.Prefix) Middleware {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip) {
				hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
				for i := len(hops) - 1; i >= 0; i-- {
					a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
					if err != nil {
						break
					}
					ip = a.Unmap()
					if !isTrusted(ip) {
						break
					}
				}
			}
			if ip.IsValid() {
				r = r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the address resolved by RealIP, if any.
func ClientIP(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip, ok
}

func clientIP(r *http.Request) string {
	if ip, ok := ClientIP(r.Context()); ok {
		return ip.String()
	}
	if ip := peerAddr(r.RemoteAddr); ip.IsValid() {
		return ip.String()
	}
	return ""
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
