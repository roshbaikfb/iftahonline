package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's plan catalog (ADR-0056). These run as app_admin.

// Catalog errors.
var (
	ErrUnknownFeature = errors.New("billing store: unknown feature")
	ErrSecondFree     = errors.New("billing store: another free plan is active")
)

// Features is the whole catalog, in display order.
func Features(ctx context.Context, tx Tx) ([]domain.Feature, error) {
	rows, err := billingdb.New(tx).ListFeatures(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: features: %w", err)
	}
	out := make([]domain.Feature, len(rows))
	for i, r := range rows {
		out[i] = domain.Feature{Key: r.Key, Kind: domain.FeatureKind(r.Kind), NameAr: r.NameAr, NameEn: r.NameEn, Sort: r.Sort}
	}
	return out, nil
}

// FeatureKind is the kind of the catalog's key; ok is false when the key
// is new.
func FeatureKind(ctx context.Context, tx Tx, key string) (k domain.FeatureKind, ok bool, err error) {
	v, err := billingdb.New(tx).GetFeatureKind(ctx, key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("billing store: feature kind: %w", err)
	}
	return domain.FeatureKind(v), true, nil
}

// SaveFeature adds a key or renames and reorders one.
func SaveFeature(ctx context.Context, tx Tx, f domain.Feature) error {
	if err := billingdb.New(tx).UpsertFeature(ctx, billingdb.UpsertFeatureParams{
		Key: f.Key, Kind: string(f.Kind), NameAr: f.NameAr, NameEn: f.NameEn, Sort: f.Sort,
	}); err != nil {
		return fmt.Errorf("billing store: save feature: %w", err)
	}
	return nil
}

// Plans are every version of every plan, with their prices, grants and
// fees, in four queries whatever the number of plans.
func Plans(ctx context.Context, tx Tx) ([]domain.PlanVersion, error) {
	q := billingdb.New(tx)
	rows, err := q.ListPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: plans: %w", err)
	}
	out := make([]domain.PlanVersion, len(rows))
	byID := make(map[ulid.ID]*domain.PlanVersion, len(rows))
	for i, r := range rows {
		kind, kindErr := domain.ParseKind(r.Kind)
		if kindErr != nil {
			return nil, kindErr
		}
		out[i] = domain.PlanVersion{Plan: domain.Plan{ID: r.ID, Code: r.Code, Version: r.Version, Kind: kind, NameAr: r.NameAr, NameEn: r.NameEn,
			TrialDays: r.TrialDays, Public: r.Public, Active: r.Active, Sort: r.Sort}, Grants: domain.Grants{}}
		byID[r.ID] = &out[i]
	}
	prices, err := q.ListAllPlanPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: plan prices: %w", err)
	}
	for _, p := range prices {
		if v := byID[p.PlanID]; v != nil {
			v.Prices = append(v.Prices, domain.Price{Currency: p.Currency, Cycle: domain.Cycle(p.Cycle), AmountMinor: p.AmountMinor})
		}
	}
	grants, err := q.ListAllPlanGrants(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: plan grants: %w", err)
	}
	for _, g := range grants {
		if v := byID[g.PlanID]; v != nil {
			v.Grants[g.Feature] = grantOf(g.Value)
		}
	}
	fees, err := q.ListAllPlanFees(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: plan fees: %w", err)
	}
	for _, f := range fees {
		if v := byID[f.PlanID]; v != nil {
			v.Fees = append(v.Fees, domain.CurrencyFee{Currency: f.Currency,
				FeeRule: domain.FeeRule{RateBps: f.RateBps, MinMinor: f.MinMinor, MaxMinor: f.MaxMinor}})
		}
	}
	return out, nil
}

func grantOf(v *int64) domain.Grant {
	if v == nil {
		return domain.Grant{Unlimited: true}
	}
	return domain.Grant{Value: *v}
}

// LockPlanCode serializes publications of code until tx ends.
func LockPlanCode(ctx context.Context, tx Tx, code string) error {
	if err := billingdb.New(tx).LockPlanCode(ctx, code); err != nil {
		return fmt.Errorf("billing store: lock plan: %w", err)
	}
	return nil
}

// LatestPlanVersion is code's newest version and its kind; ok is false
// for a new code.
func LatestPlanVersion(ctx context.Context, tx Tx, code string) (version int32, kind domain.Kind, ok bool, err error) {
	r, err := billingdb.New(tx).LatestPlanVersion(ctx, code)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, "", false, nil
	case err != nil:
		return 0, "", false, fmt.Errorf("billing store: latest plan version: %w", err)
	}
	kind, err = domain.ParseKind(r.Kind)
	return r.Version, kind, err == nil, err
}

// ActivePlanKind is the kind of code's version on sale; ok is false when
// none is.
func ActivePlanKind(ctx context.Context, tx Tx, code string) (kind domain.Kind, ok bool, err error) {
	v, err := billingdb.New(tx).GetActivePlanKind(ctx, code)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("billing store: active plan: %w", err)
	}
	kind, err = domain.ParseKind(v)
	return kind, err == nil, err
}

// RetirePlan takes code's active version off sale; it reports whether
// one was.
func RetirePlan(ctx context.Context, tx Tx, code string) (bool, error) {
	n, err := billingdb.New(tx).RetireActivePlan(ctx, code)
	if err != nil {
		return false, fmt.Errorf("billing store: retire plan: %w", err)
	}
	return n > 0, nil
}

// InsertPlanVersion writes v (its ID and version set) with its prices,
// grants and fees. An unknown feature key is ErrUnknownFeature; a second
// active free plan is ErrSecondFree.
func InsertPlanVersion(ctx context.Context, tx Tx, v domain.PlanVersion) error {
	q := billingdb.New(tx)
	err := q.InsertPlan(ctx, billingdb.InsertPlanParams{ID: v.ID, Code: v.Code, Version: v.Version, Kind: string(v.Kind),
		NameAr: v.NameAr, NameEn: v.NameEn, TrialDays: v.TrialDays, Public: v.Public, Sort: v.Sort})
	if postgres.IsUniqueViolation(err, "plans_active_free_key") {
		return ErrSecondFree
	}
	if err != nil {
		return fmt.Errorf("billing store: insert plan: %w", err)
	}
	for _, p := range v.Prices {
		if err = q.InsertPlanPrice(ctx, billingdb.InsertPlanPriceParams{PlanID: v.ID, Currency: p.Currency, Cycle: string(p.Cycle), AmountMinor: p.AmountMinor}); err != nil {
			return fmt.Errorf("billing store: insert plan price: %w", err)
		}
	}
	for key, g := range v.Grants {
		var value *int64
		if !g.Unlimited {
			value = &g.Value
		}
		err = q.InsertPlanGrant(ctx, billingdb.InsertPlanGrantParams{PlanID: v.ID, Feature: key, Value: value})
		if postgres.IsForeignKeyViolation(err, "plan_entitlements_feature_fkey") {
			return fmt.Errorf("%w %q", ErrUnknownFeature, key)
		}
		if err != nil {
			return fmt.Errorf("billing store: insert plan grant: %w", err)
		}
	}
	for _, f := range v.Fees {
		if err = q.InsertPlanFee(ctx, billingdb.InsertPlanFeeParams{PlanID: v.ID, Currency: f.Currency, RateBps: f.RateBps,
			MinMinor: f.MinMinor, MaxMinor: f.MaxMinor}); err != nil {
			return fmt.Errorf("billing store: insert plan fee: %w", err)
		}
	}
	return nil
}
