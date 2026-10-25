// Package store adapts the sqlc queries in onboardingdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/store/onboardingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = onboardingdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("onboarding store: %w", err)
	}
	return id.ID, nil
}

// Start opens the wizard of the store in ctx; a second call is a no-op.
func Start(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = onboardingdb.New(tx).StartOnboarding(ctx, store); err != nil {
		return fmt.Errorf("onboarding store: start: %w", err)
	}
	return nil
}

// LockApply serializes the store's apply jobs until tx ends.
func LockApply(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = onboardingdb.New(tx).LockApply(ctx, store); err != nil {
		return fmt.Errorf("onboarding store: lock apply: %w", err)
	}
	return nil
}

// Get returns the wizard's state; a store without one gets the zero
// State (completed).
func Get(ctx context.Context, tx Tx) (domain.State, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.State{}, err
	}
	row, err := onboardingdb.New(tx).GetOnboarding(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.State{Template: domain.TemplateNone}, nil
	}
	if err != nil {
		return domain.State{}, fmt.Errorf("onboarding store: get: %w", err)
	}
	return toState(row), nil
}

// Choose records the merchant's choice while the wizard is open; ok is
// false when it is not.
func Choose(ctx context.Context, tx Tx, c domain.Choice) (st domain.State, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return domain.State{}, false, err
	}
	activity, look := string(c.Activity), string(c.Look)
	row, err := onboardingdb.New(tx).ChooseTemplate(ctx, onboardingdb.ChooseTemplateParams{
		Activity: &activity, Look: &look, Color: &c.Color, StoreID: store,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.State{}, false, nil
	}
	if err != nil {
		return domain.State{}, false, fmt.Errorf("onboarding store: choose: %w", err)
	}
	return toState(row), true, nil
}

// Finish records the apply job's outcome for choice number seq; ok is
// false when a later choice replaced it (or it was already finished).
// version 0 is stored as none.
func Finish(ctx context.Context, tx Tx, seq int, status domain.TemplateStatus, version int) (ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	var v *int32
	if version > 0 {
		v32 := int32(min(version, 1<<30)) //nolint:gosec // bounded above
		v = &v32
	}
	_, err = onboardingdb.New(tx).FinishTemplate(ctx, onboardingdb.FinishTemplateParams{
		TemplateStatus: string(status), TemplateVersion: v, StoreID: store, TemplateSeq: int32(min(seq, 1<<30)), //nolint:gosec // bounded above
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("onboarding store: finish: %w", err)
	}
	return true, nil
}

// Complete closes the wizard; ok is false when the store has none.
func Complete(ctx context.Context, tx Tx) (st domain.State, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return domain.State{}, false, err
	}
	row, err := onboardingdb.New(tx).CompleteOnboarding(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.State{}, false, nil
	}
	if err != nil {
		return domain.State{}, false, fmt.Errorf("onboarding store: complete: %w", err)
	}
	return toState(row), true, nil
}

func toState(r onboardingdb.StoreOnboarding) domain.State {
	st := domain.State{Started: true, Template: domain.TemplateStatus(r.TemplateStatus), Seq: int(r.TemplateSeq), CompletedAt: r.CompletedAt}
	if r.Activity != nil && r.Look != nil && r.Color != nil {
		st.Choice = &domain.Choice{Activity: domain.Activity(*r.Activity), Look: domain.Look(*r.Look), Color: *r.Color}
	}
	if r.TemplateVersion != nil {
		st.TemplateVersion = int(*r.TemplateVersion)
	}
	return st
}
