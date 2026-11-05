package app

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

var errNoCollection = errs.Unavailable(0, errors.New("admin: the collection settings are not wired"))

// CollectionSettings are how pay-as-you-sell merchants may pay their
// fees (ADR-0083).
func (s *Service) CollectionSettings(ctx context.Context) (billing.CollectionSettings, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return billing.CollectionSettings{}, errNoSession
	}
	if s.d.Collection == nil {
		return billing.CollectionSettings{}, errNoCollection
	}
	return s.d.Collection.CollectionSettings(ctx)
}

// SaveCollectionSettings writes the ways and the minimums given, recorded
// first (refused settings leave nothing to record). A currency left out
// keeps its minimum (ADR-0090), so the saved settings are returned.
func (s *Service) SaveCollectionSettings(ctx context.Context, prepaid, card bool, minimums map[string]int64, rawReason string) (billing.CollectionSettings, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return billing.CollectionSettings{}, errNoSession
	}
	if s.d.Collection == nil {
		return billing.CollectionSettings{}, errNoCollection
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return billing.CollectionSettings{}, err
	}
	current, err := s.d.Collection.CollectionSettings(ctx)
	if err != nil {
		return billing.CollectionSettings{}, err
	}
	c := current.Merge(prepaid, card, minimums)
	if err = c.Validate(); err != nil {
		return billing.CollectionSettings{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.collection_saved", "billing_settings", "collection",
		map[string]any{"reason": reason, "prepaid": c.Prepaid, "card": c.Card, "minimums": c.Minimums}); err != nil {
		return billing.CollectionSettings{}, err
	}
	return c, s.d.Collection.SaveCollectionSettings(ctx, c)
}

// StoreBalance is store id's prepaid balance and its latest entries.
func (s *Service) StoreBalance(ctx context.Context, id tenant.StoreID) (billing.Balance, error) {
	if s.d.Billing == nil {
		return billing.Balance{}, errNoBilling
	}
	_, sctx, err := s.storeCtx(ctx, id)
	if err != nil {
		return billing.Balance{}, err
	}
	return s.d.Billing.StoreBalance(sctx)
}

// CreditStoreBalance adds to store id's balance, recorded first in the
// admin log with the reason, the reference and the note.
func (s *Service) CreditStoreBalance(ctx context.Context, id tenant.StoreID, amount int64, reason, reference, rawNote string) (billing.Balance, error) {
	if s.d.Billing == nil {
		return billing.Balance{}, errNoBilling
	}
	who, sctx, err := s.storeCtx(ctx, id)
	if err != nil {
		return billing.Balance{}, err
	}
	note, err := domain.ValidateReason(rawNote)
	if err != nil {
		return billing.Balance{}, err
	}
	c, err := billing.ParseCredit(amount, reason, reference)
	if err != nil {
		return billing.Balance{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "balance.credited", "store", id.String(),
		map[string]any{"amount_minor": c.AmountMinor, "reason": string(c.Reason), "reference": c.Reference, "note": note}); err != nil {
		return billing.Balance{}, err
	}
	return s.d.Billing.CreditBalance(sctx, c, who.AdminID)
}
