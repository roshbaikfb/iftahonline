package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// AdminListPaymentRules implements StrictServerInterface.
func (h *Handlers) AdminListPaymentRules(ctx context.Context, _ AdminListPaymentRulesRequestObject) (AdminListPaymentRulesResponseObject, error) {
	rules, err := h.svc.PaymentRules(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListPaymentRules200JSONResponse{Items: make([]AdminPaymentRule, len(rules)), Gateways: billing.PaymentGateways()}
	for i, r := range rules {
		out.Items[i] = toPaymentRule(r)
	}
	return out, nil
}

// AdminSavePaymentRule implements StrictServerInterface.
func (h *Handlers) AdminSavePaymentRule(ctx context.Context, req AdminSavePaymentRuleRequestObject) (AdminSavePaymentRuleResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	r := billing.PaymentRule{Country: req.Country, BillingGateway: string(req.Body.BillingGateway), StoreGateways: make([]string, len(req.Body.StoreGateways))}
	for i, g := range req.Body.StoreGateways {
		r.StoreGateways[i] = string(g)
	}
	if err := h.svc.SavePaymentRule(ctx, r, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminSavePaymentRule200JSONResponse(toPaymentRule(r)), nil
}

// AdminRemovePaymentRule implements StrictServerInterface.
func (h *Handlers) AdminRemovePaymentRule(ctx context.Context, req AdminRemovePaymentRuleRequestObject) (AdminRemovePaymentRuleResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.RemovePaymentRule(ctx, req.Country, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminRemovePaymentRule204Response{}, nil
}

func toPaymentRule(r billing.PaymentRule) AdminPaymentRule {
	out := AdminPaymentRule{Country: r.Country, BillingGateway: AdminPaymentRuleBillingGateway(r.BillingGateway),
		StoreGateways: make([]AdminPaymentRuleStoreGateways, len(r.StoreGateways))}
	for i, g := range r.StoreGateways {
		out.StoreGateways[i] = AdminPaymentRuleStoreGateways(g)
	}
	return out
}

// AdminListTaxes implements StrictServerInterface.
func (h *Handlers) AdminListTaxes(ctx context.Context, _ AdminListTaxesRequestObject) (AdminListTaxesResponseObject, error) {
	taxes, err := h.svc.Taxes(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListTaxes200JSONResponse{Items: make([]AdminTax, len(taxes))}
	for i, t := range taxes {
		out.Items[i] = AdminTax{Country: t.Country, RateBps: int(t.RateBps), NameAr: t.NameAr, NameEn: t.NameEn}
	}
	return out, nil
}

// AdminSaveTax implements StrictServerInterface.
func (h *Handlers) AdminSaveTax(ctx context.Context, req AdminSaveTaxRequestObject) (AdminSaveTaxResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	t := billing.Tax{Country: req.Country, RateBps: int32(b.RateBps), NameAr: b.NameAr, NameEn: b.NameEn} //nolint:gosec // the contract bounds it
	if err := h.svc.SaveTax(ctx, t, b.Reason); err != nil {
		return nil, err
	}
	return AdminSaveTax200JSONResponse{Country: t.Country, RateBps: int(t.RateBps), NameAr: t.NameAr, NameEn: t.NameEn}, nil
}

// AdminRemoveTax implements StrictServerInterface.
func (h *Handlers) AdminRemoveTax(ctx context.Context, req AdminRemoveTaxRequestObject) (AdminRemoveTaxResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.RemoveTax(ctx, req.Country, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminRemoveTax204Response{}, nil
}

// AdminListFXRates implements StrictServerInterface.
func (h *Handlers) AdminListFXRates(ctx context.Context, _ AdminListFXRatesRequestObject) (AdminListFXRatesResponseObject, error) {
	rates, err := h.svc.FXRates(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListFXRates200JSONResponse{Items: make([]AdminFXRate, len(rates))}
	for i, r := range rates {
		out.Items[i] = AdminFXRate{Currency: AdminFXRateCurrency(r.Currency), PerUsdMicros: r.PerUSDMicros}
	}
	return out, nil
}

// AdminSaveFXRate implements StrictServerInterface.
func (h *Handlers) AdminSaveFXRate(ctx context.Context, req AdminSaveFXRateRequestObject) (AdminSaveFXRateResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	r := billing.FXRate{Currency: string(req.Currency), PerUSDMicros: req.Body.PerUsdMicros}
	if err := h.svc.SaveFXRate(ctx, r, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminSaveFXRate200JSONResponse{Currency: AdminFXRateCurrency(r.Currency), PerUsdMicros: r.PerUSDMicros}, nil
}
