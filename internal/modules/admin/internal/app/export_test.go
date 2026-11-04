package app

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
)

// StepAlerts applies one check's readings, for tests that set them.
func (s *Service) StepAlerts(ctx context.Context, r map[domain.AlertKind]domain.Reading) error {
	return s.stepAlerts(ctx, r)
}

// DeliverAlerts sends what is owed, for tests.
func (s *Service) DeliverAlerts(ctx context.Context) error { return s.deliverAlerts(ctx) }

// SetNow sets the service's clock, for tests.
func (s *Service) SetNow(now func() time.Time) { s.now = now }
