package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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
	if len(b.Minimums) == 0 {
		return nil, errs.Validation(errs.FieldError{Field: "minimums", Code: "required", Message: "at least one currency's minimum"})
	}
	minimums := make(map[string]int64, len(b.Minimums))
	for _, m := range b.Minimums {
		if _, twice := minimums[string(m.Currency)]; twice {
			return nil, errs.Validation(errs.FieldError{Field: "minimums", Code: "duplicate", Message: "one minimum per currency"})
		}
		minimums[string(m.Currency)] = m.MinimumMinor
	}
	c, err := h.svc.SaveCollectionSettings(ctx, b.PrepaidEnabled, b.CardEnabled, minimums, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSaveCollectionSettings200JSONResponse(toCollectionSettings(c)), nil
}

func toCollectionSettings(c billing.CollectionSettings) AdminCollectionSettings {
	out := AdminCollectionSettings{PrepaidEnabled: c.Prepaid, CardEnabled: c.Card, Minimums: make([]AdminCollectionMinimum, 0, len(c.Minimums))}
	for _, cur := range billing.AllCurrencies() {
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
