package app

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Verdict is what the rules say of a new storefront cash order: its
// customer's band, and the action.
type Verdict struct {
	Band   domain.Band
	Action domain.Action
}

// Check scores a new order's phone and applies the store's rules
// (ADR-0066): one transaction of two queries, on the storefront's order
// path. Rules the plan no longer includes do nothing.
func (s *Service) Check(ctx context.Context, p phone.E164) (Verdict, error) {
	key := s.keyer.Key(p)
	var v Verdict
	var rules domain.Rules
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		signals, err := store.Signals(ctx, tx, [][]byte{key})
		if err != nil {
			return err
		}
		v.Band = domain.Assess(signals[string(key)], s.now()).Band
		rules, err = store.Rules(ctx, tx)
		return err
	})
	if err != nil {
		return Verdict{}, err
	}
	if v.Action = rules.Decide(v.Band); v.Action == domain.NoAction {
		return v, nil
	}
	allowed, err := s.plan.Allowed(ctx, entitlements.RiskRules)
	if err != nil {
		return Verdict{}, err
	}
	if !allowed {
		v.Action = domain.NoAction
	}
	return v, nil
}

// RulesView is the store's rules as the settings page shows them, with
// whether its plan includes them.
type RulesView struct {
	Rules     domain.Rules
	Available bool
}

// Rules returns the store's rules.
func (s *Service) Rules(ctx context.Context) (RulesView, error) {
	var v RulesView
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		v.Rules, err = store.Rules(ctx, tx)
		return err
	})
	if err != nil {
		return RulesView{}, err
	}
	v.Available, err = s.plan.Allowed(ctx, entitlements.RiskRules)
	return v, err
}

// SaveRules replaces the store's rules; the prepaid delivery, which came
// later (ADR-0067), is off when left empty. Turning one on needs the
// plan's risk_rules (403 plan_limit); turning them off never does.
func (s *Service) SaveRules(ctx context.Context, r domain.Rules) (RulesView, error) {
	if r.PrepayFrom == "" {
		r.PrepayFrom = domain.Off
	}
	if err := r.Validate(); err != nil {
		return RulesView{}, err
	}
	if r.Active() {
		if err := s.plan.Require(ctx, entitlements.RiskRules); err != nil {
			return RulesView{}, err
		}
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveRules(ctx, tx, r); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "risk.rules_updated", TargetType: "store_risk_rules",
			Metadata: map[string]any{"hold_from": string(r.HoldFrom), "verify_from": string(r.VerifyFrom), "prepay_from": string(r.PrepayFrom)}})
	})
	if err != nil {
		return RulesView{}, err
	}
	return s.Rules(ctx)
}
