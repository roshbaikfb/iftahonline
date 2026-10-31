package httpapi

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
)

// AdminGetStoreEntitlements implements StrictServerInterface.
func (h *Handlers) AdminGetStoreEntitlements(ctx context.Context, req AdminGetStoreEntitlementsRequestObject) (AdminGetStoreEntitlementsResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	g, err := h.svc.StoreGrants(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetStoreEntitlements200JSONResponse(toStoreGrants(g)), nil
}

// AdminSetStoreOverride implements StrictServerInterface.
func (h *Handlers) AdminSetStoreOverride(ctx context.Context, req AdminSetStoreOverrideRequestObject) (AdminSetStoreOverrideResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	o := entitlements.Override{Feature: entitlements.Feature(req.Feature), Reason: b.Reason}
	switch {
	case b.Unlimited != nil && *b.Unlimited:
		o.Grant.Unlimited = true
	case b.Value != nil:
		o.Grant.Value = *b.Value
	}
	if b.ExpiresAt != nil {
		o.ExpiresAt = *b.ExpiresAt
	}
	g, err := h.svc.SetOverride(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return AdminSetStoreOverride200JSONResponse(toStoreGrants(g)), nil
}

// AdminClearStoreOverride implements StrictServerInterface.
func (h *Handlers) AdminClearStoreOverride(ctx context.Context, req AdminClearStoreOverrideRequestObject) (AdminClearStoreOverrideResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	g, err := h.svc.ClearOverride(ctx, id, entitlements.Feature(req.Feature), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminClearStoreOverride200JSONResponse(toStoreGrants(g)), nil
}

func toStoreGrants(g entitlements.StoreGrants) AdminStoreEntitlements {
	now := time.Now()
	out := AdminStoreEntitlements{Plan: toGrants(g.Plan), Effective: toGrants(g.Effective), Overrides: make([]AdminOverride, len(g.Overrides))}
	for i, o := range g.Overrides {
		out.Overrides[i] = AdminOverride{Feature: string(o.Feature), Unlimited: o.Grant.Unlimited, Value: o.Grant.Value,
			Reason: o.Reason, Active: o.Active(now), UpdatedAt: o.UpdatedAt}
		if !o.ExpiresAt.IsZero() {
			at := o.ExpiresAt
			out.Overrides[i].ExpiresAt = &at
		}
	}
	return out
}

func toGrants(g entitlements.Grants) []AdminPlanGrant {
	out := make([]AdminPlanGrant, 0, len(g))
	for _, f := range slices.Sorted(maps.Keys(g)) {
		unlimited, value := g[f].Unlimited, g[f].Value
		out = append(out, AdminPlanGrant{Feature: string(f), Unlimited: &unlimited, Value: &value})
	}
	return out
}
