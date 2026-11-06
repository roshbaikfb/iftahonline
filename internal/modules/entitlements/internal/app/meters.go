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

// PlanFeatures is what the dashboard locks before the merchant tries: the
// flag features the store's plan includes, and the counted ones it has no
// room left in (one more would be refused). Monthly ones never lock: the
// store's orders are never stopped.
type PlanFeatures struct {
	Included []domain.Feature
	Full     []domain.Feature
}

// PlanFeatures reads the store in ctx's.
func (s *Service) PlanFeatures(ctx context.Context) (PlanFeatures, error) {
	meters, err := s.Meters(ctx)
	if err != nil {
		return PlanFeatures{}, err
	}
	out := PlanFeatures{Included: []domain.Feature{}, Full: []domain.Feature{}}
	for _, m := range meters {
		if m.Kind == domain.Count && !m.Unlimited && m.Used >= m.Limit {
			out.Full = append(out.Full, m.Feature)
		}
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		grants, gErr := s.grantsTx(ctx, tx)
		if gErr != nil {
			return gErr
		}
		for _, f := range flagOrder() {
			if g := grants[f]; g.Unlimited || g.Value > 0 {
				out.Included = append(out.Included, f)
			}
		}
		return nil
	})
	return out, err
}

// flagOrder lists the flag features, in a stable order.
func flagOrder() []domain.Feature {
	return []domain.Feature{domain.CardPayments, domain.Coupons, domain.Pixels, domain.Feeds, domain.AdFree, domain.RiskRules, domain.Profit}
}
