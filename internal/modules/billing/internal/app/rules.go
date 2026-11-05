package app

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Payment rules and taxes by country (ADR-0089).

var (
	errNoRuleFor    = errs.NotFound("rule_not_found", "no rule of its own for this country")
	errOthersStay   = errs.Conflict("others_stay", "the row for the other countries stays; change it instead")
	errNoRuleOthers = errs.Unavailable(0, errNoRuleFor)
)

// PaymentRules are every country's rule, the others' (ZZ) last.
func (s *Service) PaymentRules(ctx context.Context) ([]domain.PaymentRule, error) {
	var out []domain.PaymentRule
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.PaymentRules(ctx, tx)
		return txErr
	})
	return out, err
}

// PaymentRuleFor is how a merchant in country is paid: its own rule, or
// the others'.
func (s *Service) PaymentRuleFor(ctx context.Context, country string) (domain.PaymentRule, error) {
	rules, err := s.PaymentRules(ctx)
	if err != nil {
		return domain.PaymentRule{}, err
	}
	r, ok := domain.RuleFor(rules, country)
	if !ok {
		return domain.PaymentRule{}, errNoRuleOthers
	}
	return r, nil
}

// SavePaymentRule checks and writes a country's rule (the console's pool).
func (s *Service) SavePaymentRule(ctx context.Context, r domain.PaymentRule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return store.SavePaymentRule(ctx, tx, r) })
}

// RemovePaymentRule removes a country's own rule: it then follows the
// others'. The others' row stays.
func (s *Service) RemovePaymentRule(ctx context.Context, country string) error {
	if country == domain.OtherCountries {
		return errOthersStay
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		removed, err := store.DeletePaymentRule(ctx, tx, country)
		if err == nil && !removed {
			err = errNoRuleFor
		}
		return err
	})
}

// Taxes are every country's tax, the others' (ZZ) last.
func (s *Service) Taxes(ctx context.Context) ([]domain.Tax, error) {
	var out []domain.Tax
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.Taxes(ctx, tx)
		return txErr
	})
	return out, err
}

// SaveTax checks and writes a country's tax; it reaches the invoices
// written after it.
func (s *Service) SaveTax(ctx context.Context, t domain.Tax) error {
	t.NameAr, t.NameEn = strings.TrimSpace(t.NameAr), strings.TrimSpace(t.NameEn)
	if err := t.Validate(); err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return store.SaveTax(ctx, tx, t) })
}

// RemoveTax removes a country's own tax: it then pays the others'.
func (s *Service) RemoveTax(ctx context.Context, country string) error {
	if country == domain.OtherCountries {
		return errOthersStay
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		removed, err := store.DeleteTax(ctx, tx, country)
		if err == nil && !removed {
			err = errNoRuleFor
		}
		return err
	})
}
