package app

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// FeatureView is a catalog entry and whether the code enforces it.
type FeatureView struct {
	billing.Feature
	Enforced bool
}

var errNoCatalog = errs.Unavailable(0, errors.New("admin: the plan catalog is not wired"))

// catalog is the plan catalog for a signed-in admin.
func (s *Service) catalog(ctx context.Context) (Identity, billing.Catalog, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Plans == nil {
		return Identity{}, nil, errNoCatalog
	}
	return who, s.d.Plans, nil
}

func (s *Service) enforced(key string) bool { return s.d.Enforced != nil && s.d.Enforced(key) }

// Features lists the catalog.
func (s *Service) Features(ctx context.Context) ([]FeatureView, error) {
	_, c, err := s.catalog(ctx)
	if err != nil {
		return nil, err
	}
	fs, err := c.Features(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FeatureView, len(fs))
	for i, f := range fs {
		out[i] = FeatureView{Feature: f, Enforced: s.enforced(f.Key)}
	}
	return out, nil
}

// SaveFeature adds or renames a catalog entry, recorded first in the
// admin log (like setStatus: a failed change shows as an attempt).
func (s *Service) SaveFeature(ctx context.Context, f billing.Feature, rawReason string) (FeatureView, error) {
	who, c, err := s.catalog(ctx)
	if err != nil {
		return FeatureView{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return FeatureView{}, err
	}
	if err = f.Validate(); err != nil {
		return FeatureView{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "feature.saved", "feature", f.Key,
		map[string]any{"reason": reason, "kind": f.Kind, "name_en": f.NameEn}); err != nil {
		return FeatureView{}, err
	}
	if err = c.SaveFeature(ctx, f); err != nil {
		return FeatureView{}, err
	}
	return FeatureView{Feature: f, Enforced: s.enforced(f.Key)}, nil
}

// Plans lists every version of every plan.
func (s *Service) Plans(ctx context.Context) ([]billing.PlanVersion, error) {
	_, c, err := s.catalog(ctx)
	if err != nil {
		return nil, err
	}
	return c.Plans(ctx)
}

// PublishPlan puts v on sale as its code's next version, recorded first.
func (s *Service) PublishPlan(ctx context.Context, v billing.PlanVersion, rawReason string) (billing.PlanVersion, error) {
	who, c, err := s.catalog(ctx)
	if err != nil {
		return billing.PlanVersion{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return billing.PlanVersion{}, err
	}
	if err = v.Validate(); err != nil {
		return billing.PlanVersion{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "plan.published", "plan", v.Code,
		map[string]any{"reason": reason, "kind": v.Kind, "prices": len(v.Prices), "grants": len(v.Grants)}); err != nil {
		return billing.PlanVersion{}, err
	}
	return c.PublishPlan(ctx, v)
}

// RetirePlan takes code off sale, recorded first.
func (s *Service) RetirePlan(ctx context.Context, code, rawReason string) error {
	who, c, err := s.catalog(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "plan.retired", "plan", code, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.RetirePlan(ctx, code)
}
