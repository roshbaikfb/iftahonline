// Package logging builds the process logger: log/slog JSON on stdout
// (CLAUDE.md Linux runtime rules). It enriches every record with the
// request ID, trace/span IDs and store ID found in the context, and redacts
// sensitive attributes by key (CLAUDE.md security rules).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Options configures New.
type Options struct {
	Level   slog.Level
	Writer  io.Writer // os.Stdout in production
	Service string
	Role    string
	Version string
}

// New returns a JSON logger with context enrichment and redaction.
func New(o Options) *slog.Logger {
	h := slog.NewJSONHandler(o.Writer, &slog.HandlerOptions{
		Level:       o.Level,
		ReplaceAttr: redact,
	})
	return slog.New(contextHandler{h}).With(
		slog.String("service", o.Service),
		slog.String("role", o.Role),
		slog.String("version", o.Version),
	)
}

// ParseLevel accepts debug, info, warn or error (case-insensitive).
func ParseLevel(s string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("logging: level %q: %w", s, err)
	}
	return l, nil
}

type requestIDKey struct{}

// WithRequestID stores the request ID in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID from ctx, or "".
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// contextHandler adds request-scoped attributes from ctx. Like any slog
// attribute added at Handle time, they nest under groups opened with
// WithGroup; prefer flat attributes (With) on request paths.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	if store, err := tenant.FromContext(ctx); err == nil {
		r.AddAttrs(slog.String("store_id", store.String()))
	}
	if err := h.Handler.Handle(ctx, r); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	return nil
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(as)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

const redacted = "[REDACTED]"

// redact hides secrets by attribute key. Phone-like keys are masked rather
// than dropped so support can still correlate; everything else that may
// hold a credential or personal address is replaced entirely.
func redact(_ []string, a slog.Attr) slog.Attr {
	k := strings.ToLower(a.Key)
	switch {
	case containsAny(k, "password", "passwd", "secret", "token", "otp", "authorization", "cookie", "api_key", "apikey", "credential", "address", "card"):
		return slog.String(a.Key, redacted)
	case containsAny(k, "phone", "mobile", "msisdn", "whatsapp"):
		return slog.String(a.Key, maskTail(a.Value.String()))
	default:
		return a
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// maskTail keeps the first 4 and last 2 characters of s.
func maskTail(s string) string {
	r := []rune(s)
	if len(r) <= 6 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:4]) + strings.Repeat("*", len(r)-6) + string(r[len(r)-2:])
}
