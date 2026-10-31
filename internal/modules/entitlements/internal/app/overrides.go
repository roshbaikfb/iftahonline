package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// StoreGrants are what the store's plan grants, its overrides and what
// it is granted with them: the console's view of one store (ADR-0057).
type StoreGrants struct {
	Plan      domain.Grants
	Overrides []domain.Override // expired ones included, for the record
	Effective domain.Grants
}

var errNoOverride = errs.NotFound("override_not_found", "the store has no override of this feature")

// StoreGrants reads the store in ctx's grants, bypassing the cache.
func (s *Service) StoreGrants(ctx context.Context) (StoreGrants, error) {
	var out StoreGrants
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		if out.Plan, err = s.source.GrantsTx(ctx, tx); err != nil {
			return fmt.Errorf("entitlements: grants: %w", err)
		}
		if out.Overrides, err = store.Overrides(ctx, tx); err != nil {
			return err
		}
		out.Effective = out.Plan.With(out.Overrides, s.now())
		return nil
	})
	return out, err
}

// SetOverride grants the store in ctx o.Grant of o.Feature instead of its
// plan's, until o.ExpiresAt (zero: until cleared).
func (s *Service) SetOverride(ctx context.Context, o domain.Override) error {
	if err := o.Validate(s.now()); err != nil {
		return err
	}
	return s.changeGrants(ctx, o.Feature, func(tx pgx.Tx) error { return store.SaveOverride(ctx, tx, o) })
}

// ClearOverride gives the store in ctx its plan's grant of f again.
func (s *Service) ClearOverride(ctx context.Context, f domain.Feature) error {
	return s.changeGrants(ctx, f, func(tx pgx.Tx) error {
		ok, err := store.DeleteOverride(ctx, tx, f)
		if err == nil && !ok {
			return errNoOverride
		}
		return err
	})
}

// changeGrants runs change and publishes GrantsChanged in one tenant
// transaction, so every instance's cache and the storefront's pages
// follow (ADR-0057); this instance forgets the store at once.
func (s *Service) changeGrants(ctx context.Context, f domain.Feature, change func(pgx.Tx) error) error {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("entitlements: %w", err)
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := change(tx); txErr != nil {
			return txErr
		}
		meta, txErr := event.MetaFor(ctx, s.ids, domain.EventGrantsChanged, 1)
		if txErr != nil {
			return txErr
		}
		if txErr = s.events.PublishTx(ctx, tx, domain.GrantsChanged{Meta: meta, Feature: f}); txErr != nil {
			return fmt.Errorf("entitlements: publish: %w", txErr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Invalidate(id)
	return nil
}
