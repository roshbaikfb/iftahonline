package otel_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/otel"
)

func TestNoopWithoutEndpoint(t *testing.T) {
	t.Parallel()
	tel, err := otel.Setup(t.Context(), otel.Options{ServiceName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.TracerProvider.Tracer("t").Start(t.Context(), "op")
	if span.SpanContext().IsValid() {
		t.Fatal("noop provider must not produce valid spans")
	}
	span.End()
	if err := tel.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestExportsOnShutdown(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tel, err := otel.Setup(t.Context(), otel.Options{
		Endpoint: srv.URL, ServiceName: "iftahonline-test", Version: "v0", Environment: "test", SampleRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.TracerProvider.Tracer("t").Start(t.Context(), "op")
	if !span.SpanContext().IsValid() || !span.SpanContext().IsSampled() {
		t.Fatal("span must be sampled at ratio 1")
	}
	span.End()
	counter, err := tel.MeterProvider.Meter("t").Int64Counter("requests")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(t.Context(), 1)

	if err := tel.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if paths["/v1/traces"] == 0 || paths["/v1/metrics"] == 0 {
		t.Fatalf("collector got %v", paths)
	}
}

func TestBadEndpoint(t *testing.T) {
	t.Parallel()
	if _, err := otel.Setup(t.Context(), otel.Options{Endpoint: "http://[bad"}); err == nil {
		t.Fatal("expected error")
	}
}
