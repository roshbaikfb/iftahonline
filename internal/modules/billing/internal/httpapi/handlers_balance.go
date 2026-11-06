package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
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
	if b.Gateway != "" {
		g := BillingBalanceBillingGateway(b.Gateway)
		out.BillingGateway = &g
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

// SwitchToPays implements StrictServerInterface.
func (h *Handlers) SwitchToPays(ctx context.Context, req SwitchToPaysRequestObject) (SwitchToPaysResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b, err := h.withCollection(ctx, h.svc.SwitchToPays, req.Body.Collection)
	if err != nil {
		return nil, err
	}
	return SwitchToPays200JSONResponse(b), nil
}

// SetBillingCollection implements StrictServerInterface.
func (h *Handlers) SetBillingCollection(ctx context.Context, req SetBillingCollectionRequestObject) (SetBillingCollectionResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b, err := h.withCollection(ctx, h.svc.SetCollection, req.Body.Collection)
	if err != nil {
		return nil, err
	}
	return SetBillingCollection200JSONResponse(b), nil
}

// withCollection authorizes billing.manage, applies the way c through do
// and answers with the store's billing now.
func (h *Handlers) withCollection(ctx context.Context, do func(context.Context, domain.Collection) error, c BillingCollectionInputCollection) (Billing, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return Billing{}, err
	}
	if err := do(ctx, domain.Collection(c)); err != nil {
		return Billing{}, err
	}
	s, err := h.svc.Summary(ctx)
	if err != nil {
		return Billing{}, err
	}
	return toBilling(s), nil
}
