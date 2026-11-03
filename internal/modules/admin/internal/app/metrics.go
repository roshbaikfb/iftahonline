package app

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
)

// Metrics are the revenue and growth on the console's home (ADR-0075). A
// section that could not be read is nil and named in Errors.
type Metrics struct {
	ComputedAt time.Time
	Revenue    []billing.Revenue // nil when unread; empty when nothing pays
	NewStores  *tenancy.NewStores
	Errors     []string
}

// Metrics reads the monthly recurring revenue and the new stores. Like
// Health, a failure is logged and named, never returned.
func (s *Service) Metrics(ctx context.Context) (Metrics, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return Metrics{}, errNoSession
	}
	now := s.now().UTC()
	m := Metrics{ComputedAt: now}
	check := func(section string, f func(context.Context) error) {
		cctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		if err := f(cctx); err != nil {
			s.d.Log.WarnContext(ctx, "admin: metrics failed", "section", section, "err", err)
			m.Errors = append(m.Errors, section)
		}
	}
	check("revenue", func(ctx context.Context) error {
		if s.d.Billing == nil {
			return errNoBilling
		}
		r, err := s.d.Billing.Revenue(ctx)
		if err == nil {
			m.Revenue = append([]billing.Revenue{}, r...)
		}
		return err
	})
	check("new_stores", func(ctx context.Context) error {
		n, err := s.d.Directory.NewStores(ctx, now)
		if err == nil {
			m.NewStores = &n
		}
		return err
	})
	return m, nil
}
