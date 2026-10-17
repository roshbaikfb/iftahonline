package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %q: %v", buf.String(), err)
	}
	return m
}

func TestContextEnrichment(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := logging.New(logging.Options{Writer: &buf, Level: slog.LevelInfo, Service: "iftahonline", Role: "api", Version: "test"})

	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	ctx := logging.WithRequestID(t.Context(), "req-1")
	ctx = tenant.WithStore(ctx, store)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{4, 5, 6},
		TraceFlags: trace.FlagsSampled,
	})
	ctx = trace.ContextWithSpanContext(ctx, sc)

	log.With("k", "v").InfoContext(ctx, "hello")
	m := decode(t, &buf)
	for k, want := range map[string]string{
		"msg": "hello", "service": "iftahonline", "role": "api", "build_version": "test",
		"request_id": "req-1", "store_id": store.String(), "trace_id": sc.TraceID().String(), "span_id": sc.SpanID().String(),
	} {
		if got, _ := m[k].(string); got != want {
			t.Errorf("%s = %v, want %q (record %v)", k, m[k], want, m)
		}
	}
}

func TestNoContextAttrs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logging.New(logging.Options{Writer: &buf}).InfoContext(t.Context(), "x")
	m := decode(t, &buf)
	for _, k := range []string{"request_id", "store_id", "trace_id"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected %s", k)
		}
	}
	if logging.RequestID(t.Context()) != "" {
		t.Error("RequestID on empty ctx must be empty")
	}
}

func TestRedaction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key, value, want string
	}{
		{"password", "hunter2", "[REDACTED]"},
		{"AccessToken", "abc", "[REDACTED]"},
		{"otp_code", "123456", "[REDACTED]"},
		{"Authorization", "Bearer x", "[REDACTED]"},
		{"shipping_address", "12 Nile St", "[REDACTED]"},
		{"phone", "+201012345678", "+201*******78"},
		{"customer_mobile", "0101", "****"},
		{"sku", "ABC-1", "ABC-1"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logging.New(logging.Options{Writer: &buf}).Info("x", tt.key, tt.value)
			m := decode(t, &buf)
			if got := m[tt.key]; got != tt.want {
				t.Fatalf("%s = %v, want %q", tt.key, got, tt.want)
			}
			if tt.want != tt.value && strings.Contains(buf.String(), tt.value) && len(tt.value) > 4 {
				t.Fatalf("raw value leaked: %s", buf.String())
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		got, err := logging.ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := logging.ParseLevel("loud"); err == nil {
		t.Error("ParseLevel(loud) must fail")
	}
}
