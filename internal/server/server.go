// Package server wires the HTTP stack shared by every role: routing on
// net/http's ServeMux, the middleware chain, the system endpoints from the
// OpenAPI contract, and graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/otel"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server/apigen"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

// Deps are the handler's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Log       *slog.Logger
	IDs       *ulid.Generator
	Telemetry *otel.Telemetry
	HTTP      config.HTTP
	Health    *Health
	Profile   middleware.Profile
	// Mount registers role-specific routes (modules, storefront pages).
	// Unmatched paths get a 404 problem unless Mount registers a more
	// specific catch-all such as "GET /".
	Mount func(Routes)
}

// Routes is what a Mount function receives.
type Routes struct {
	Mux      *http.ServeMux
	Problems *problem.Responder
	Log      *slog.Logger
}

// NewHandler builds the full handler: routes plus the middleware chain.
func NewHandler(d Deps) http.Handler {
	resp := problem.NewResponder(d.Log)
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}

	mux := http.NewServeMux()
	system := apigen.NewStrictHandlerWithOptions(d.Health, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	apigen.HandlerWithOptions(system, apigen.StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: badRequest})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp.Write(w, r, errs.NotFound("route_not_found", "no route for this path"))
	})
	if d.Mount != nil {
		d.Mount(Routes{Mux: mux, Problems: resp, Log: d.Log})
	}

	traced := otelhttp.NewMiddleware("http.server",
		otelhttp.WithTracerProvider(d.Telemetry.TracerProvider),
		otelhttp.WithMeterProvider(d.Telemetry.MeterProvider),
		otelhttp.WithPropagators(d.Telemetry.Propagator),
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/livez" && r.URL.Path != "/readyz" }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }),
	)

	return middleware.Chain(mux,
		middleware.RequestID(d.IDs),
		middleware.RealIP(d.HTTP.TrustedProxies),
		middleware.Middleware(traced),
		middleware.AccessLog(d.Log),
		middleware.Recover(d.Log, resp),
		middleware.SecurityHeaders(d.Profile),
		middleware.MaxBody(d.HTTP.MaxBodyBytes, resp),
		middleware.Timeout(d.HTTP.RequestTimeout),
	)
}

// NewServer applies the configured timeouts and limits.
func NewServer(c config.HTTP, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              c.Addr,
		Handler:           h,
		ReadHeaderTimeout: c.ReadHeaderTimeout,
		ReadTimeout:       c.ReadTimeout,
		WriteTimeout:      c.WriteTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Serve runs srv on ln until ctx is cancelled (SIGTERM), then shuts down
// gracefully: /readyz turns 503, the process waits DrainDelay so load
// balancers notice, then in-flight requests get up to ShutdownTimeout.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, h *Health, c config.HTTP, log *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	log.InfoContext(ctx, "http server listening", "addr", ln.Addr().String())

	select {
	case err := <-errCh:
		return fmt.Errorf("server: serve: %w", err)
	case <-ctx.Done():
	}

	h.SetDraining()
	log.InfoContext(ctx, "draining", "delay", c.DrainDelay.String())
	if c.DrainDelay > 0 {
		t := time.NewTimer(c.DrainDelay)
		select {
		case <-t.C:
		case err := <-errCh:
			t.Stop()
			return fmt.Errorf("server: serve: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server: shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: serve: %w", err)
	}
	log.InfoContext(ctx, "http server stopped")
	return nil
}
