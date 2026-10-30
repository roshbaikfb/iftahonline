// Package app holds the billing use cases (ADR-0045). In 7a that is
// answering what the store in ctx is granted: its plan's grants while
// the plan applies, the free plan's otherwise.
package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Service implements the use cases.
type Service struct {
	db  postgres.Beginner
	now func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB postgres.Beginner
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: d.DB, now: now}
}

// Grants are what the store in ctx is granted now, in a transaction of
// its own.
func (s *Service) Grants(ctx context.Context) (g domain.Grants, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		g, txErr = s.GrantsTx(ctx, tx)
		return txErr
	})
	return g, err
}

// GrantsTx are what the store in ctx is granted now, read in its tenant
// transaction tx: two queries, three when the free plan applies.
func (s *Service) GrantsTx(ctx context.Context, tx pgx.Tx) (domain.Grants, error) {
	sub, ok, err := store.Subscription(ctx, tx)
	if err != nil {
		return nil, err
	}
	plan := sub.PlanID
	if !ok || !sub.PlanApplies(s.now()) {
		free, fErr := store.FreePlan(ctx, tx)
		if fErr != nil {
			return nil, fErr
		}
		plan = free.ID
	}
	return store.PlanGrants(ctx, tx, plan)
}
