package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Catalog is the console's plan catalog (ADR-0056): the features, and
// every version of every plan. It runs on the console's pool (app_admin);
// merchant code only reads plans.
type Catalog struct {
	db  postgres.Beginner
	ids *ulid.Generator
}

// NewCatalog builds the catalog on db, the console's pool.
func NewCatalog(db postgres.Beginner, ids *ulid.Generator) *Catalog {
	return &Catalog{db: db, ids: ids}
}

var (
	errKindFixed    = errs.Conflict("feature_kind_fixed", "a feature's kind cannot change")
	errPlanKind     = errs.Conflict("plan_kind_fixed", "a plan's kind cannot change between versions")
	errSecondFree   = errs.Conflict("free_plan_exists", "another free plan is on sale; publish a version of it instead")
	errFreeRequired = errs.Conflict("free_plan_required", "stores fall back to the free plan: it cannot be retired")
	errNoPlan       = errs.NotFound("plan_not_found", "no plan with this code is on sale")
)

// Features lists the catalog.
func (c *Catalog) Features(ctx context.Context) (out []domain.Feature, err error) {
	err = postgres.InTx(ctx, c.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.Features(ctx, tx)
		return txErr
	})
	return out, err
}

// SaveFeature adds a key, or renames and reorders one; its kind is fixed.
func (c *Catalog) SaveFeature(ctx context.Context, f domain.Feature) error {
	if err := f.Validate(); err != nil {
		return err
	}
	return postgres.InTx(ctx, c.db, func(tx pgx.Tx) error {
		kind, ok, err := store.FeatureKind(ctx, tx, f.Key)
		if err != nil {
			return err
		}
		if ok && kind != f.Kind {
			return errKindFixed
		}
		return store.SaveFeature(ctx, tx, f)
	})
}

// Plans lists every version of every plan.
func (c *Catalog) Plans(ctx context.Context) (out []domain.PlanVersion, err error) {
	err = postgres.InTx(ctx, c.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.Plans(ctx, tx)
		return txErr
	})
	return out, err
}

// PublishPlan puts v on sale as the next version of its code (the first
// for a new code), retiring the version on sale. Stores keep the version
// they bought and renew into the new one (ADR-0056). A code keeps its
// kind; one free plan is on sale at a time.
func (c *Catalog) PublishPlan(ctx context.Context, v domain.PlanVersion) (domain.PlanVersion, error) {
	if err := v.Validate(); err != nil {
		return domain.PlanVersion{}, err
	}
	err := postgres.InTx(ctx, c.db, func(tx pgx.Tx) error {
		features, err := store.Features(ctx, tx)
		if err != nil {
			return err
		}
		if err = checkGrants(v.Grants, features); err != nil {
			return err
		}
		if err = store.LockPlanCode(ctx, tx, v.Code); err != nil {
			return err
		}
		latest, kind, ok, err := store.LatestPlanVersion(ctx, tx, v.Code)
		if err != nil {
			return err
		}
		if ok && kind != v.Kind {
			return errPlanKind
		}
		if _, err = store.RetirePlan(ctx, tx, v.Code); err != nil {
			return err
		}
		v.ID, v.Version, v.Active = c.ids.New(), latest+1, true
		err = store.InsertPlanVersion(ctx, tx, v)
		switch {
		case errors.Is(err, store.ErrSecondFree):
			return errSecondFree
		case errors.Is(err, store.ErrUnknownFeature):
			return errs.Validation(errs.FieldError{Field: "grants", Code: "unknown", Message: err.Error()})
		}
		return err
	})
	if err != nil {
		return domain.PlanVersion{}, err
	}
	return v, nil
}

// checkGrants: every key is in the catalog, and a flag is 0 or 1.
func checkGrants(g domain.Grants, catalog []domain.Feature) error {
	kinds := make(map[string]domain.FeatureKind, len(catalog))
	for _, f := range catalog {
		kinds[f.Key] = f.Kind
	}
	var fe []errs.FieldError
	for key, v := range g {
		kind, ok := kinds[key]
		switch {
		case !ok:
			fe = append(fe, errs.FieldError{Field: "grants." + key, Code: "unknown", Message: "not in the features catalog"})
		case kind == domain.FeatureFlag && (v.Unlimited || v.Value > 1):
			fe = append(fe, errs.FieldError{Field: "grants." + key, Code: "invalid", Message: "a flag is 0 or 1"})
		}
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// RetirePlan takes code off sale. Stores on it keep it, and renew into
// the same version; the free plan cannot be retired, only replaced by a
// new version.
func (c *Catalog) RetirePlan(ctx context.Context, code string) error {
	return postgres.InTx(ctx, c.db, func(tx pgx.Tx) error {
		if err := store.LockPlanCode(ctx, tx, code); err != nil {
			return err
		}
		kind, ok, err := store.ActivePlanKind(ctx, tx, code)
		switch {
		case err != nil:
			return err
		case !ok:
			return errNoPlan
		case kind == domain.KindFree:
			return errFreeRequired
		}
		if _, err = store.RetirePlan(ctx, tx, code); err != nil {
			return fmt.Errorf("billing: retire: %w", err)
		}
		return nil
	})
}
