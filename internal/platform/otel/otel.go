// Package otel sets up OpenTelemetry tracing and metrics exported over
// OTLP/HTTP. Providers are returned and injected explicitly; this package
// never sets the otel globals (CLAUDE.md rule 4). With no endpoint
// configured everything is a no-op, so local runs and tests need no
// collector.
package otel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Options configures Setup.
type Options struct {
	Endpoint    string  // e.g. http://otel-collector:4318; empty disables export
	ServiceName string  // e.g. iftahonline-api
	Version     string  // build version
	Environment string  // development, staging, production
	SampleRatio float64 // parent-based ratio sampler for new traces
}

// Telemetry holds the providers to inject into instrumented components.
type Telemetry struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	shutdown       []func(context.Context) error
}

// Shutdown flushes and stops exporters. Call it during graceful shutdown.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdown {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("otel: shutdown: %w", err)
	}
	return nil
}

// Setup builds the providers. Exporters connect lazily, so Setup is fast
// and does not fail when the collector is down.
func Setup(ctx context.Context, o Options) (*Telemetry, error) {
	t := &Telemetry{
		Propagator: propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
	}
	if o.Endpoint == "" {
		t.TracerProvider = tracenoop.NewTracerProvider()
		t.MeterProvider = metricnoop.NewMeterProvider()
		return t, nil
	}
	u, err := url.Parse(o.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("otel: endpoint: %w", err)
	}

	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(o.ServiceName),
		semconv.ServiceVersion(o.Version),
		semconv.DeploymentEnvironmentName(o.Environment),
	)

	traceOpts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(u.JoinPath("/v1/traces").String())}
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(u.JoinPath("/v1/metrics").String())}

	traceExp, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("otel: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(o.SampleRatio))),
	)

	metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("otel: metric exporter: %w", err), tp.Shutdown(ctx))
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
		sdkmetric.WithResource(res),
	)

	t.TracerProvider, t.MeterProvider = tp, mp
	t.shutdown = []func(context.Context) error{tp.Shutdown, mp.Shutdown}
	return t, nil
}
