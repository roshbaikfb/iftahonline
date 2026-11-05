package store

import (
	"context"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
)

// PaymentRules are every country's payment rule, ZZ (the others) last.
func PaymentRules(ctx context.Context, tx Tx) ([]domain.PaymentRule, error) {
	rows, err := billingdb.New(tx).ListPaymentRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: payment rules: %w", err)
	}
	out := make([]domain.PaymentRule, len(rows))
	for i, r := range rows {
		out[i] = domain.PaymentRule{Country: r.Country, BillingGateway: r.BillingGateway, StoreGateways: r.StoreGateways}
	}
	return out, nil
}

// SavePaymentRule writes a country's rule (the console's role).
func SavePaymentRule(ctx context.Context, tx Tx, r domain.PaymentRule) error {
	gw := r.StoreGateways
	if gw == nil {
		gw = []string{}
	}
	if err := billingdb.New(tx).SavePaymentRule(ctx, billingdb.SavePaymentRuleParams{Country: r.Country, BillingGateway: r.BillingGateway,
		StoreGateways: gw}); err != nil {
		return fmt.Errorf("billing store: save payment rule: %w", err)
	}
	return nil
}

// DeletePaymentRule removes a country's own rule, never the others';
// removed is false when there was none.
func DeletePaymentRule(ctx context.Context, tx Tx, country string) (removed bool, err error) {
	n, err := billingdb.New(tx).DeletePaymentRule(ctx, country)
	if err != nil {
		return false, fmt.Errorf("billing store: delete payment rule: %w", err)
	}
	return n == 1, nil
}

// Taxes are every country's tax, ZZ (the others) last.
func Taxes(ctx context.Context, tx Tx) ([]domain.Tax, error) {
	rows, err := billingdb.New(tx).ListTaxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: taxes: %w", err)
	}
	out := make([]domain.Tax, len(rows))
	for i, r := range rows {
		out[i] = domain.Tax{Country: r.Country, RateBps: r.RateBps, NameAr: r.NameAr, NameEn: r.NameEn}
	}
	return out, nil
}

// SaveTax writes a country's tax (the console's role).
func SaveTax(ctx context.Context, tx Tx, t domain.Tax) error {
	if err := billingdb.New(tx).SaveTax(ctx, billingdb.SaveTaxParams{Country: t.Country, RateBps: t.RateBps, NameAr: t.NameAr,
		NameEn: t.NameEn}); err != nil {
		return fmt.Errorf("billing store: save tax: %w", err)
	}
	return nil
}

// DeleteTax removes a country's own tax, never the others'.
func DeleteTax(ctx context.Context, tx Tx, country string) (removed bool, err error) {
	n, err := billingdb.New(tx).DeleteTax(ctx, country)
	if err != nil {
		return false, fmt.Errorf("billing store: delete tax: %w", err)
	}
	return n == 1, nil
}
