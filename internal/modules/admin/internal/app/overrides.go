package app

import (
	"context"
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

var errNoOverrides = errs.Unavailable(0, errors.New("admin: per-store feature flags are not wired"))

// inStore is ctx inside store id for a signed-in admin, once the store is
// known to exist (404 otherwise).
func (s *Service) inStore(ctx context.Context, id tenant.StoreID) (Identity, context.Context, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Overrides == nil {
		return Identity{}, nil, errNoOverrides
	}
	if _, _, err := s.d.Directory.Store(ctx, id); err != nil {
		return Identity{}, nil, err
	}
	return who, tenant.WithStore(ctx, id), nil
}

// StoreGrants are what store id's plan grants, its overrides and the
// result (ADR-0057).
func (s *Service) StoreGrants(ctx context.Context, id tenant.StoreID) (entitlements.StoreGrants, error) {
	_, sctx, err := s.inStore(ctx, id)
	if err != nil {
		return entitlements.StoreGrants{}, err
	}
	return s.d.Overrides.StoreGrants(sctx)
}

// SetOverride grants store id o instead of its plan's grant, recorded
// first in the admin log with the reason (which the override keeps).
func (s *Service) SetOverride(ctx context.Context, id tenant.StoreID, o entitlements.Override) (entitlements.StoreGrants, error) {
	who, sctx, err := s.inStore(ctx, id)
	if err != nil {
		return entitlements.StoreGrants{}, err
	}
	if o.Reason, err = domain.ValidateReason(o.Reason); err != nil {
		return entitlements.StoreGrants{}, err
	}
	if err = o.Validate(s.now()); err != nil {
		return entitlements.StoreGrants{}, err
	}
	meta := map[string]any{"reason": o.Reason, "feature": o.Feature, "unlimited": o.Grant.Unlimited, "value": o.Grant.Value}
	if !o.ExpiresAt.IsZero() {
		meta["expires_at"] = o.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if err = s.audit(ctx, &who.AdminID, "store.feature_overridden", "store", id.String(), meta); err != nil {
		return entitlements.StoreGrants{}, err
	}
	if err = s.d.Overrides.SetOverride(sctx, o); err != nil {
		return entitlements.StoreGrants{}, err
	}
	return s.d.Overrides.StoreGrants(sctx)
}

// ClearOverride gives store id its plan's grant of f again, recorded
// first.
func (s *Service) ClearOverride(ctx context.Context, id tenant.StoreID, f entitlements.Feature, rawReason string) (entitlements.StoreGrants, error) {
	who, sctx, err := s.inStore(ctx, id)
	if err != nil {
		return entitlements.StoreGrants{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return entitlements.StoreGrants{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "store.feature_override_cleared", "store", id.String(),
		map[string]any{"reason": reason, "feature": f}); err != nil {
		return entitlements.StoreGrants{}, err
	}
	if err = s.d.Overrides.ClearOverride(sctx, f); err != nil {
		return entitlements.StoreGrants{}, err
	}
	return s.d.Overrides.StoreGrants(sctx)
}
