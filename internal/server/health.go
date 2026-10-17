package server

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/server/apigen"
)

// BuildInfo identifies the running binary. Version and Commit are set at
// link time (-ldflags -X) in cmd/platform.
type BuildInfo struct {
	Version string
	Commit  string
	Role    string
}

// Check is one readiness dependency.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Health implements the system endpoints of the OpenAPI contract:
// liveness, readiness and build info.
type Health struct {
	log      *slog.Logger
	build    BuildInfo
	checks   []Check
	timeout  time.Duration
	draining atomic.Bool
}

var _ apigen.StrictServerInterface = (*Health)(nil)

// NewHealth returns a Health with the given readiness checks.
func NewHealth(log *slog.Logger, build BuildInfo, checks ...Check) *Health {
	return &Health{log: log, build: build, checks: checks, timeout: time.Second}
}

// SetDraining makes /readyz fail so load balancers stop sending traffic
// before the server shuts down.
func (h *Health) SetDraining() { h.draining.Store(true) }

// Draining reports whether shutdown has begun.
func (h *Health) Draining() bool { return h.draining.Load() }

// GetLivez reports that the process is alive. It never checks dependencies,
// so a database outage does not get healthy processes restarted.
func (h *Health) GetLivez(context.Context, apigen.GetLivezRequestObject) (apigen.GetLivezResponseObject, error) {
	return apigen.GetLivez200JSONResponse{Status: apigen.Ok}, nil
}

// GetReadyz runs every check concurrently, bounded by a short timeout.
func (h *Health) GetReadyz(ctx context.Context, _ apigen.GetReadyzRequestObject) (apigen.GetReadyzResponseObject, error) {
	if h.Draining() {
		return apigen.GetReadyz503JSONResponse{Status: apigen.Draining}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	results := make(map[string]string, len(h.checks))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
		ok = true
	)
	for _, c := range h.checks {
		wg.Go(func() {
			err := c.Fn(ctx)
			state := "ok"
			if err != nil {
				state = "fail"
				if errors.Is(err, context.DeadlineExceeded) {
					state = "timeout"
				}
				h.log.WarnContext(ctx, "readiness check failed", "check", c.Name, "err", err)
			}
			mu.Lock()
			defer mu.Unlock()
			results[c.Name] = state
			ok = ok && err == nil
		})
	}
	wg.Wait()

	if !ok {
		return apigen.GetReadyz503JSONResponse{Status: apigen.Unavailable, Checks: &results}, nil
	}
	return apigen.GetReadyz200JSONResponse{Status: apigen.Ok, Checks: &results}, nil
}

// GetVersion returns build information.
func (h *Health) GetVersion(context.Context, apigen.GetVersionRequestObject) (apigen.GetVersionResponseObject, error) {
	return apigen.GetVersion200JSONResponse{
		Version:   h.build.Version,
		Commit:    h.build.Commit,
		GoVersion: runtime.Version(),
		Role:      apigen.VersionInfoRole(h.build.Role),
	}, nil
}
