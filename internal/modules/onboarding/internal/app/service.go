// Package app holds the setup wizard's use cases (ADR-0034): opening it
// at sign-up, recording the merchant's choice, applying the starter
// template in a job, and closing it.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Templates applies a starter template to the store in ctx and returns
// its version. It must be idempotent: a job may run it twice.
type Templates interface {
	Apply(ctx context.Context, c domain.Choice) (version int, err error)
}

// ApplyTemplateArgs applies choice number Seq of the store's wizard.
type ApplyTemplateArgs struct {
	jobs.TenantArgs
	Seq int `json:"seq"`
}

// Kind implements river.JobArgs.
func (ApplyTemplateArgs) Kind() string { return "onboarding.apply_template" }

// applyAttempts bounds the retries before the wizard shows the failure.
const applyAttempts = 5

// Service implements the use cases.
type Service struct {
	db        postgres.Beginner
	jobs      jobs.Inserter
	templates Templates
	audit     audit.Recorder
}

// Deps are the service's dependencies.
type Deps struct {
	DB        postgres.Beginner
	Jobs      jobs.Inserter
	Templates Templates
	Audit     audit.Recorder
}

// NewService builds the service. A nil Audit discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	return &Service{db: d.DB, jobs: d.Jobs, templates: d.Templates, audit: d.Audit}
}

// StartTx opens the wizard of the store in ctx, in sign-up's transaction.
func (s *Service) StartTx(ctx context.Context, tx pgx.Tx) error { return store.Start(ctx, tx) }

// Get returns the wizard of the store in ctx.
func (s *Service) Get(ctx context.Context) (st domain.State, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		st, txErr = store.Get(ctx, tx)
		return txErr
	})
	return st, err
}

// Choose records the merchant's choice and enqueues its apply job, in one
// transaction (ADR-0003). It is refused once the wizard is closed.
func (s *Service) Choose(ctx context.Context, c domain.Choice) (domain.State, error) {
	c, err := domain.ValidateChoice(c)
	if err != nil {
		return domain.State{}, err
	}
	ta, err := jobs.ForTenant(ctx)
	if err != nil {
		return domain.State{}, err
	}
	var st domain.State
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var ok bool
		var txErr error
		if st, ok, txErr = store.Choose(ctx, tx, c); txErr != nil {
			return txErr
		}
		if !ok {
			return domain.ErrClosed
		}
		if _, txErr = s.jobs.InsertTx(ctx, tx, ApplyTemplateArgs{TenantArgs: ta, Seq: st.Seq}, &river.InsertOpts{MaxAttempts: applyAttempts}); txErr != nil {
			return fmt.Errorf("onboarding: enqueue apply: %w", txErr)
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "onboarding.template_chosen", TargetType: "store", TargetID: ta.StoreID.String(),
			Metadata: map[string]any{"activity": string(c.Activity), "look": string(c.Look), "color": c.Color}})
	})
	return st, err
}

// ApplyTemplate runs ApplyTemplateArgs. Jobs of one store run one at a
// time, and a job whose choice was replaced does nothing, so the last
// choice is the one applied. On the last attempt a failure is recorded
// for the wizard instead of retried.
func (s *Service) ApplyTemplate(ctx context.Context, seq int, lastAttempt bool) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.LockApply(ctx, tx); err != nil {
			return err
		}
		st, err := store.Get(ctx, tx)
		if err != nil {
			return err
		}
		if st.Seq != seq || st.Template != domain.TemplatePreparing || st.Choice == nil {
			return nil
		}
		version, applyErr := s.templates.Apply(ctx, *st.Choice)
		if applyErr != nil && !lastAttempt {
			return fmt.Errorf("onboarding: apply template: %w", applyErr)
		}
		status := domain.TemplateReady
		if applyErr != nil {
			status, version = domain.TemplateFailed, 0
		}
		_, err = store.Finish(ctx, tx, seq, status, version)
		return err
	})
}

// Complete closes the wizard. It is idempotent, and a store without a
// wizard is already complete.
func (s *Service) Complete(ctx context.Context) (st domain.State, err error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.State{}, err
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		before, txErr := store.Get(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if !before.Started || before.CompletedAt != nil {
			st = before
			return nil
		}
		var ok bool
		if st, ok, txErr = store.Complete(ctx, tx); txErr != nil {
			return txErr
		}
		if !ok {
			return errors.New("onboarding: the wizard vanished while closing it")
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "onboarding.completed", TargetType: "store", TargetID: storeID.String()})
	})
	return st, err
}
