package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Counter counts what a store holds of a counted feature, in its tenant
// transaction: the owning module's count, the one AdmitTx admits
// against (cmd/platform wires catalog, identity and tenancy's).
type Counter func(ctx context.Context, tx pgx.Tx) (int64, error)

// Meter is one feature's usage for the merchant's billing page
// (ADR-0061): this month's count of a monthly feature, what the store
// holds of a counted one, against what it is granted.
type Meter struct {
	Feature   domain.Feature
	Kind      domain.Kind
	Used      int64
	Limit     int64 // when not Unlimited
	Unlimited bool
}

// meterOrder is the billing page's order.
func meterOrder() []domain.Feature {
	return []domain.Feature{domain.OrdersPerMonth, domain.Messages, domain.Products, domain.Staff, domain.CustomDomains}
}

// Meters reads the store-in-ctx's meters in one tenant transaction: its
// grants, each monthly count and each holding. A counted feature without
// a counter is left out.
func (s *Service) Meters(ctx context.Context) ([]Meter, error) {
	var out []Meter
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		grants, err := s.grantsTx(ctx, tx)
		if err != nil {
			return err
		}
		period := domain.PeriodOf(s.now())
		for _, f := range meterOrder() {
			g := grants[f]
			m := Meter{Feature: f, Kind: f.Kind(), Limit: g.Value, Unlimited: g.Unlimited}
			switch m.Kind {
			case domain.Monthly:
				if m.Used, err = store.Used(ctx, tx, f, period); err != nil {
					return err
				}
			default:
				count, ok := s.counters[f]
				if !ok {
					continue
				}
				if m.Used, err = count(ctx, tx); err != nil {
					return fmt.Errorf("entitlements: count %s: %w", f, err)
				}
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}
