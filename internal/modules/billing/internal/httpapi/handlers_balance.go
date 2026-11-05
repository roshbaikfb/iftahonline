package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
)

// GetBillingBalance implements StrictServerInterface.
func (h *Handlers) GetBillingBalance(ctx context.Context, _ GetBillingBalanceRequestObject) (GetBillingBalanceResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	b, err := h.svc.StoreBalance(ctx)
	if err != nil {
		return nil, err
	}
	out := GetBillingBalance200JSONResponse{Currency: BillingBalanceCurrency(b.Currency), BalanceMinor: b.Minor, MinimumMinor: b.MinimumMinor,
		Level: BillingBalanceLevel(b.Level), Offered: make([]BillingBalanceOffered, len(b.Offered)), Entries: make([]BillingBalanceEntry, len(b.Entries))}
	if b.Collection != "" {
		c := BillingBalanceCollection(b.Collection)
		out.Collection = &c
	}
	for i, o := range b.Offered {
		out.Offered[i] = BillingBalanceOffered(o)
	}
	for i, e := range b.Entries {
		o := BillingBalanceEntry{Id: e.ID.String(), Kind: BillingBalanceEntryKind(e.Kind), AmountMinor: e.AmountMinor, BalanceAfterMinor: e.AfterMinor,
			CreatedAt: e.CreatedAt}
		if e.Reference != "" {
			ref := e.Reference
			o.Reference = &ref
		}
		out.Entries[i] = o
	}
	return out, nil
}

// TopUpBillingBalance implements StrictServerInterface.
func (h *Handlers) TopUpBillingBalance(ctx context.Context, req TopUpBillingBalanceRequestObject) (TopUpBillingBalanceResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	u, err := h.svc.TopUp(ctx, req.Body.AmountMinor, app.Payer{})
	if err != nil {
		return nil, err
	}
	return TopUpBillingBalance200JSONResponse{CheckoutUrl: u}, nil
}
