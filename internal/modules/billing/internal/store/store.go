// Package store adapts the sqlc queries in billingdb to domain types.
// Functions on tenant tables run on a transaction opened by
// postgres.InTenantTx and take the store ID from tenant.FromContext,
// never from their arguments (CLAUDE.md rule 5); RLS checks it a second
// time. Plans are platform data, readable from any transaction.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the transaction the functions run on.
type Tx = billingdb.DBTX

// Subscription is the store-in-ctx's subscription; ok is false when it
// has none (the free plan).
func Subscription(ctx context.Context, tx Tx) (s domain.Subscription, ok bool, err error) {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return s, false, fmt.Errorf("billing store: %w", err)
	}
	r, err := billingdb.New(tx).GetSubscription(ctx, store.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return s, false, nil
	case err != nil:
		return s, false, fmt.Errorf("billing store: get subscription: %w", err)
	}
	if s.Kind, err = domain.ParseKind(r.Kind); err != nil {
		return s, false, fmt.Errorf("billing store: %w", err)
	}
	if s.Status, err = domain.ParseStatus(r.Status); err != nil {
		return s, false, fmt.Errorf("billing store: %w", err)
	}
	if r.Cycle != nil {
		if s.Cycle, err = domain.ParseCycle(*r.Cycle); err != nil {
			return s, false, fmt.Errorf("billing store: %w", err)
		}
	}
	s.PlanID, s.CancelAtPeriodEnd, s.TrialUsed = r.PlanID, r.CancelAtPeriodEnd, r.TrialUsed
	s.TrialEndsAt, s.PeriodStart, s.PeriodEnd = at(r.TrialEndsAt), at(r.PeriodStart), at(r.PeriodEnd)
	s.GraceUntil, s.SuspendedAt = at(r.GraceUntil), at(r.SuspendedAt)
	return s, true, nil
}

func at(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// FreePlan is the plan stores fall back to.
func FreePlan(ctx context.Context, tx Tx) (domain.Plan, error) {
	r, err := billingdb.New(tx).GetFreePlan(ctx)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("billing store: free plan: %w", err)
	}
	return domain.Plan{
		ID: r.ID, Code: r.Code, Version: r.Version, Kind: domain.KindFree, NameAr: r.NameAr, NameEn: r.NameEn,
		TrialDays: r.TrialDays, Public: r.Public, Active: r.Active, Sort: r.Sort,
	}, nil
}

// PlanGrants are what a plan grants, by feature key.
func PlanGrants(ctx context.Context, tx Tx, plan ulid.ID) (domain.Grants, error) {
	rows, err := billingdb.New(tx).ListPlanGrants(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("billing store: plan grants: %w", err)
	}
	g := make(domain.Grants, len(rows))
	for _, r := range rows {
		if r.Value == nil {
			g[r.Feature] = domain.Grant{Unlimited: true}
		} else {
			g[r.Feature] = domain.Grant{Value: *r.Value}
		}
	}
	return g, nil
}

// SaveSubscription writes the store-in-ctx's subscription after a
// lifecycle step.
func SaveSubscription(ctx context.Context, tx Tx, s domain.Subscription) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	var cycle *string
	if s.Cycle != "" {
		c := string(s.Cycle)
		cycle = &c
	}
	if err = billingdb.New(tx).SaveSubscription(ctx, billingdb.SaveSubscriptionParams{
		StoreID: store, PlanID: s.PlanID, Status: string(s.Status), Cycle: cycle,
		TrialEndsAt: ptr(s.TrialEndsAt), PeriodStart: ptr(s.PeriodStart), PeriodEnd: ptr(s.PeriodEnd),
		GraceUntil: ptr(s.GraceUntil), SuspendedAt: ptr(s.SuspendedAt),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd, TrialUsed: s.TrialUsed,
	}); err != nil {
		return fmt.Errorf("billing store: save subscription: %w", err)
	}
	return nil
}

// ptr is a time for a nullable column: nil when unset.
func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// PeriodsEnded are the stores whose trial or cancelled period ended by
// now (app.periods_ended).
func PeriodsEnded(ctx context.Context, tx Tx, now time.Time) ([]ulid.ID, error) {
	ids, err := billingdb.New(tx).PeriodsEnded(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("billing store: periods ended: %w", err)
	}
	return ids, nil
}

// Health counts subscriptions by status and open and overdue invoices
// of every store and account (ADR-0058).
type Health struct {
	Trialing, Active, PastDue, Suspended, Cancelled int64
	InvoicesOpen, InvoicesOverdue                   int64
}

// PlatformHealth reads Health through app.billing_health, which returns
// numbers only.
func PlatformHealth(ctx context.Context, tx Tx, now time.Time) (Health, error) {
	r, err := billingdb.New(tx).BillingHealth(ctx, now)
	if err != nil {
		return Health{}, fmt.Errorf("billing store: health: %w", err)
	}
	return Health{Trialing: r.Trialing, Active: r.Active, PastDue: r.PastDue, Suspended: r.Suspended, Cancelled: r.Cancelled,
		InvoicesOpen: r.InvoicesOpen, InvoicesOverdue: r.InvoicesOverdue}, nil
}
