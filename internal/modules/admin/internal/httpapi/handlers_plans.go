package httpapi

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// AdminListFeatures implements StrictServerInterface.
func (h *Handlers) AdminListFeatures(ctx context.Context, _ AdminListFeaturesRequestObject) (AdminListFeaturesResponseObject, error) {
	fs, err := h.svc.Features(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListFeatures200JSONResponse{Items: make([]AdminFeature, len(fs))}
	for i, f := range fs {
		out.Items[i] = toFeature(f)
	}
	return out, nil
}

// AdminSaveFeature implements StrictServerInterface.
func (h *Handlers) AdminSaveFeature(ctx context.Context, req AdminSaveFeatureRequestObject) (AdminSaveFeatureResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	f := billing.Feature{Key: req.Key, Kind: billing.FeatureKind(b.Kind), NameAr: b.NameAr, NameEn: b.NameEn}
	if b.Sort != nil {
		f.Sort = int32(*b.Sort) //nolint:gosec // display order; out-of-range values only reorder
	}
	saved, err := h.svc.SaveFeature(ctx, f, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSaveFeature200JSONResponse(toFeature(saved)), nil
}

// AdminListPlans implements StrictServerInterface.
func (h *Handlers) AdminListPlans(ctx context.Context, _ AdminListPlansRequestObject) (AdminListPlansResponseObject, error) {
	ps, err := h.svc.Plans(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListPlans200JSONResponse{Items: make([]AdminPlan, len(ps))}
	for i, p := range ps {
		out.Items[i] = toPlan(p)
	}
	return out, nil
}

// AdminPublishPlan implements StrictServerInterface.
func (h *Handlers) AdminPublishPlan(ctx context.Context, req AdminPublishPlanRequestObject) (AdminPublishPlanResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	v, err := fromPlanInput(*req.Body)
	if err != nil {
		return nil, err
	}
	pub, err := h.svc.PublishPlan(ctx, v, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminPublishPlan201JSONResponse(toPlan(pub)), nil
}

// AdminRetirePlan implements StrictServerInterface.
func (h *Handlers) AdminRetirePlan(ctx context.Context, req AdminRetirePlanRequestObject) (AdminRetirePlanResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.RetirePlan(ctx, req.Code, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminRetirePlan204Response{}, nil
}

func toFeature(f app.FeatureView) AdminFeature {
	return AdminFeature{Key: f.Key, Kind: AdminFeatureKind(f.Kind), NameAr: f.NameAr, NameEn: f.NameEn, Sort: int(f.Sort), Enforced: f.Enforced}
}

func toPlan(p billing.PlanVersion) AdminPlan {
	out := AdminPlan{Id: p.ID.String(), Code: p.Code, Version: int(p.Version), Kind: AdminPlanKind(p.Kind), NameAr: p.NameAr, NameEn: p.NameEn,
		TrialDays: int(p.TrialDays), Public: p.Public, Sort: int(p.Sort), Active: p.Active,
		Prices: make([]AdminPlanPrice, len(p.Prices)), Grants: make([]AdminPlanGrant, 0, len(p.Grants)), Fees: make([]AdminPlanFee, len(p.Fees))}
	for i, pr := range p.Prices {
		out.Prices[i] = AdminPlanPrice{Currency: AdminPlanPriceCurrency(pr.Currency), Cycle: AdminPlanPriceCycle(pr.Cycle), AmountMinor: pr.AmountMinor}
	}
	for _, key := range slices.Sorted(maps.Keys(p.Grants)) {
		g := p.Grants[key]
		unlimited, value := g.Unlimited, g.Value
		out.Grants = append(out.Grants, AdminPlanGrant{Feature: key, Unlimited: &unlimited, Value: &value})
	}
	for i, f := range p.Fees {
		minMinor := f.MinMinor
		out.Fees[i] = AdminPlanFee{Currency: AdminPlanFeeCurrency(f.Currency), RateBps: int(f.RateBps), MinMinor: &minMinor, MaxMinor: f.MaxMinor}
	}
	return out
}

// fromPlanInput is the version an admin asked for; a feature listed
// twice is refused here, as the grants are a map below.
func fromPlanInput(in AdminPlanInput) (billing.PlanVersion, error) {
	v := billing.PlanVersion{Plan: billing.Plan{Code: in.Code, Kind: billing.Kind(in.Kind), NameAr: in.NameAr, NameEn: in.NameEn, Public: true},
		Grants: make(billing.Grants, len(in.Grants))}
	if in.TrialDays != nil {
		v.TrialDays = int32(min(max(*in.TrialDays, -1), 1000)) //nolint:gosec // clamped; the domain checks the range
	}
	if in.Public != nil {
		v.Public = *in.Public
	}
	if in.Sort != nil {
		v.Sort = int32(*in.Sort) //nolint:gosec // display order
	}
	if in.Prices != nil {
		for _, p := range *in.Prices {
			v.Prices = append(v.Prices, billing.Price{Currency: string(p.Currency), Cycle: billing.Cycle(p.Cycle), AmountMinor: p.AmountMinor})
		}
	}
	for i, g := range in.Grants {
		if _, dup := v.Grants[g.Feature]; dup {
			return billing.PlanVersion{}, errs.Validation(errs.FieldError{Field: fmt.Sprintf("grants[%d]", i), Code: "duplicate", Message: "a feature is listed once"})
		}
		var grant billing.Grant
		switch {
		case g.Unlimited != nil && *g.Unlimited:
			grant.Unlimited = true
		case g.Value != nil:
			grant.Value = *g.Value
		}
		v.Grants[g.Feature] = grant
	}
	if in.Fees != nil {
		for _, f := range *in.Fees {
			fee := billing.CurrencyFee{Currency: string(f.Currency), FeeRule: billing.FeeRule{RateBps: int32(min(max(f.RateBps, -1), 10001)), MaxMinor: f.MaxMinor}} //nolint:gosec // clamped
			if f.MinMinor != nil {
				fee.MinMinor = *f.MinMinor
			}
			v.Fees = append(v.Fees, fee)
		}
	}
	return v, nil
}
