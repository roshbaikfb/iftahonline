package app

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// Payment rules and taxes by country (ADR-0089), through billing's
// console (the console's role), each change recorded first with its
// reason.

func (s *Service) collection(ctx context.Context) (Identity, billing.CollectionConsole, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Collection == nil {
		return Identity{}, nil, errNoCollection
	}
	return who, s.d.Collection, nil
}

// PaymentRules are every country's rule, the others' last.
func (s *Service) PaymentRules(ctx context.Context) ([]billing.PaymentRule, error) {
	_, c, err := s.collection(ctx)
	if err != nil {
		return nil, err
	}
	return c.PaymentRules(ctx)
}

// SavePaymentRule writes a country's rule, recorded first.
func (s *Service) SavePaymentRule(ctx context.Context, r billing.PaymentRule, rawReason string) error {
	who, c, err := s.collection(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = r.Validate(); err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.payment_rule_saved", "payment_rule", r.Country,
		map[string]any{"reason": reason, "billing_gateway": r.BillingGateway, "store_gateways": r.StoreGateways}); err != nil {
		return err
	}
	return c.SavePaymentRule(ctx, r)
}

// RemovePaymentRule removes a country's own rule, recorded first.
func (s *Service) RemovePaymentRule(ctx context.Context, country, rawReason string) error {
	who, c, err := s.collection(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.payment_rule_removed", "payment_rule", country, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.RemovePaymentRule(ctx, country)
}

// Taxes are every country's tax, the others' last.
func (s *Service) Taxes(ctx context.Context) ([]billing.Tax, error) {
	_, c, err := s.collection(ctx)
	if err != nil {
		return nil, err
	}
	return c.Taxes(ctx)
}

// SaveTax writes a country's tax, recorded first.
func (s *Service) SaveTax(ctx context.Context, t billing.Tax, rawReason string) error {
	who, c, err := s.collection(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = t.Validate(); err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.tax_saved", "tax", t.Country,
		map[string]any{"reason": reason, "rate_bps": t.RateBps}); err != nil {
		return err
	}
	return c.SaveTax(ctx, t)
}

// RemoveTax removes a country's own tax, recorded first.
func (s *Service) RemoveTax(ctx context.Context, country, rawReason string) error {
	who, c, err := s.collection(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.tax_removed", "tax", country, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return c.RemoveTax(ctx, country)
}

// FXRates are each store currency's rate to the dollar (ADR-0090).
func (s *Service) FXRates(ctx context.Context) ([]billing.FXRate, error) {
	_, c, err := s.collection(ctx)
	if err != nil {
		return nil, err
	}
	return c.FXRates(ctx)
}

// SaveFXRate writes a currency's rate, recorded first with the one it
// replaces.
func (s *Service) SaveFXRate(ctx context.Context, r billing.FXRate, rawReason string) error {
	who, c, err := s.collection(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = r.Validate(); err != nil {
		return err
	}
	rates, err := c.FXRates(ctx)
	if err != nil {
		return err
	}
	details := map[string]any{"reason": reason, "per_usd_micros": r.PerUSDMicros}
	for _, old := range rates {
		if old.Currency == r.Currency {
			details["was_per_usd_micros"] = old.PerUSDMicros
		}
	}
	if err = s.audit(ctx, &who.AdminID, "billing.fx_rate_saved", "fx_rate", r.Currency, details); err != nil {
		return err
	}
	return c.SaveFXRate(ctx, r)
}
