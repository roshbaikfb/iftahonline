package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// AdminGetCollectionSettings implements StrictServerInterface.
func (h *Handlers) AdminGetCollectionSettings(ctx context.Context, _ AdminGetCollectionSettingsRequestObject) (AdminGetCollectionSettingsResponseObject, error) {
	c, err := h.svc.CollectionSettings(ctx)
	if err != nil {
		return nil, err
	}
	return AdminGetCollectionSettings200JSONResponse(toCollectionSettings(c)), nil
}

// AdminSaveCollectionSettings implements StrictServerInterface.
func (h *Handlers) AdminSaveCollectionSettings(ctx context.Context, req AdminSaveCollectionSettingsRequestObject) (AdminSaveCollectionSettingsResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	c := billing.CollectionSettings{Prepaid: b.PrepaidEnabled, Card: b.CardEnabled, Minimums: make(map[string]int64, len(b.Minimums))}
	for _, m := range b.Minimums {
		c.Minimums[string(m.Currency)] = m.MinimumMinor
	}
	if err := h.svc.SaveCollectionSettings(ctx, c, b.Reason); err != nil {
		return nil, err
	}
	return AdminSaveCollectionSettings200JSONResponse(toCollectionSettings(c)), nil
}

func toCollectionSettings(c billing.CollectionSettings) AdminCollectionSettings {
	out := AdminCollectionSettings{PrepaidEnabled: c.Prepaid, CardEnabled: c.Card, Minimums: make([]AdminCollectionMinimum, 0, len(c.Minimums))}
	for _, cur := range billing.Currencies() {
		if m, ok := c.Minimums[cur]; ok {
			out.Minimums = append(out.Minimums, AdminCollectionMinimum{Currency: AdminCollectionMinimumCurrency(cur), MinimumMinor: m})
		}
	}
	return out
}

// AdminGetStoreBalance implements StrictServerInterface.
func (h *Handlers) AdminGetStoreBalance(ctx context.Context, req AdminGetStoreBalanceRequestObject) (AdminGetStoreBalanceResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	b, err := h.svc.StoreBalance(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetStoreBalance200JSONResponse(toStoreBalance(b)), nil
}

// AdminCreditStoreBalance implements StrictServerInterface.
func (h *Handlers) AdminCreditStoreBalance(ctx context.Context, req AdminCreditStoreBalanceRequestObject) (AdminCreditStoreBalanceResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	in := req.Body
	b, err := h.svc.CreditStoreBalance(ctx, id, in.AmountMinor, string(in.Reason), in.Reference, in.Note)
	if err != nil {
		return nil, err
	}
	return AdminCreditStoreBalance200JSONResponse(toStoreBalance(b)), nil
}

func toStoreBalance(b billing.Balance) AdminStoreBalance {
	out := AdminStoreBalance{Currency: AdminStoreBalanceCurrency(b.Currency), BalanceMinor: b.Minor, MinimumMinor: b.MinimumMinor,
		Level: AdminStoreBalanceLevel(b.Level), Entries: make([]AdminBalanceEntry, len(b.Entries))}
	if b.Collection != "" {
		c := AdminStoreBalanceCollection(b.Collection)
		out.Collection = &c
	}
	for i, e := range b.Entries {
		o := AdminBalanceEntry{Id: e.ID.String(), Kind: AdminBalanceEntryKind(e.Kind), AmountMinor: e.AmountMinor, BalanceAfterMinor: e.AfterMinor,
			CreatedAt: e.CreatedAt}
		if e.Reason != "" {
			r := AdminBalanceEntryReason(e.Reason)
			o.Reason = &r
		}
		if e.Reference != "" {
			ref := e.Reference
			o.Reference = &ref
		}
		if !e.Admin.IsZero() {
			a := e.Admin.String()
			o.AdminId = &a
		}
		out.Entries[i] = o
	}
	return out
}
