package app

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
)

// healthTimeout bounds each check, so a stuck dependency shows as down
// instead of hanging the page.
const healthTimeout = 3 * time.Second

// Health is the platform at a glance (ADR-0058). A section that could
// not be read is nil and named in Errors.
type Health struct {
	CheckedAt time.Time
	Database  bool
	Redis     bool
	Stores    *tenancy.StoreCounts
	Billing   *billing.Health
	Jobs      *store.JobHealth
	Errors    []string
}

// Health checks the database and Redis and reads the counts. Failures
// are logged and reported, never returned: the page is for when things
// are broken.
func (s *Service) Health(ctx context.Context) (Health, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return Health{}, errNoSession
	}
	h := Health{CheckedAt: s.now().UTC()}
	fail := func(section string, err error) {
		s.d.Log.WarnContext(ctx, "admin: health check failed", "section", section, "err", err)
		h.Errors = append(h.Errors, section)
	}
	check := func(section string, f func(context.Context) error) {
		cctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		if err := f(cctx); err != nil {
			fail(section, err)
		}
	}

	check("stores", func(ctx context.Context) error {
		c, err := s.d.Directory.Counts(ctx)
		if err == nil {
			h.Stores, h.Database = &c, true
		}
		return err
	})
	if !h.Database {
		h.Errors = append(h.Errors, "database")
	}
	check("jobs", func(ctx context.Context) error {
		j, err := store.Jobs(ctx, s.d.DB, s.now())
		if err == nil {
			h.Jobs = &j
		}
		return err
	})
	check("redis", func(ctx context.Context) error {
		if s.d.Redis == nil {
			return errNoRedis
		}
		err := s.d.Redis.Ping(ctx).Err()
		h.Redis = err == nil
		return err
	})
	check("billing", func(ctx context.Context) error {
		if s.d.Billing == nil {
			return errNoBilling
		}
		b, err := s.d.Billing.Health(ctx)
		if err == nil {
			h.Billing = &b
		}
		return err
	})
	return h, nil
}
