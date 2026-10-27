package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errSizeChartNotFound = errs.NotFound("size_chart_not_found", "size chart not found")
	errSizeChartLimit    = errs.Conflict("size_chart_limit", fmt.Sprintf("a store can have at most %d size charts", domain.MaxSizeChartsPerStore))
)

// ListSizeCharts returns the store's size charts, by name, with how many
// products use each.
func (s *Service) ListSizeCharts(ctx context.Context) ([]domain.SizeChart, error) {
	var out []domain.SizeChart
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListSizeCharts(ctx, tx)
		return txErr
	})
	return out, err
}

// GetSizeChart returns one size chart.
func (s *Service) GetSizeChart(ctx context.Context, id ulid.ID) (domain.SizeChart, error) {
	var c domain.SizeChart
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = store.GetSizeChart(ctx, tx, id)
		return txErr
	})
	return c, notFound(err, errSizeChartNotFound)
}

// CreateSizeChart adds a size chart (at most domain.MaxSizeChartsPerStore).
// No product uses it yet, so no page changes.
func (s *Service) CreateSizeChart(ctx context.Context, in domain.SizeChartInput) (domain.SizeChart, error) {
	in, err := domain.ValidateSizeChart(in)
	if err != nil {
		return domain.SizeChart{}, err
	}
	var c domain.SizeChart
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		n, txErr := store.CountSizeCharts(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if n >= domain.MaxSizeChartsPerStore {
			return errSizeChartLimit
		}
		if c, txErr = store.SaveSizeChart(ctx, tx, s.ids.New(), in, true); txErr != nil {
			return txErr
		}
		return s.auditSizeChart(ctx, tx, "size_chart.created", c)
	})
	return c, err
}

// UpdateSizeChart replaces a size chart; every product using it shows the
// new one.
func (s *Service) UpdateSizeChart(ctx context.Context, id ulid.ID, in domain.SizeChartInput) (domain.SizeChart, error) {
	in, err := domain.ValidateSizeChart(in)
	if err != nil {
		return domain.SizeChart{}, err
	}
	var c domain.SizeChart
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, txErr := store.SaveSizeChart(ctx, tx, id, in, false); txErr != nil {
			return txErr
		}
		var txErr error
		if c, txErr = store.GetSizeChart(ctx, tx, id); txErr != nil {
			return txErr
		}
		if txErr = s.events.sizeChartChanged(ctx, tx, id, domain.SizeChartUpdated); txErr != nil {
			return txErr
		}
		return s.auditSizeChart(ctx, tx, "size_chart.updated", c)
	})
	return c, notFound(err, errSizeChartNotFound)
}

// DeleteSizeChart deletes a size chart; its products simply lose it.
func (s *Service) DeleteSizeChart(ctx context.Context, id ulid.ID) error {
	return deleteOne(ctx, s.db, id, store.SizeChartOnly, store.DeleteSizeChart, func(tx pgx.Tx, c domain.SizeChart) error {
		if err := s.events.sizeChartChanged(ctx, tx, id, domain.SizeChartDeleted); err != nil {
			return err
		}
		return s.auditSizeChart(ctx, tx, "size_chart.deleted", c)
	}, errSizeChartNotFound)
}

func (s *Service) auditSizeChart(ctx context.Context, tx pgx.Tx, action string, c domain.SizeChart) error {
	return s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "size_chart", TargetID: c.ID.String(),
		Metadata: map[string]any{"name": c.Name}})
}
