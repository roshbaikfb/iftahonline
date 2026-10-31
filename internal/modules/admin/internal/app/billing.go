package app

import (
	"context"
	"errors"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errNoBilling = errs.Unavailable(0, errors.New("admin: billing is not wired"))
	errNoRedis   = errors.New("admin: no redis")
	errReference = errs.Validation(errs.FieldError{Field: "reference", Code: "invalid", Message: "1 to 120 characters"})
)

// storeCtx is ctx inside store id for a signed-in admin, once the store
// is known to exist (404 otherwise).
func (s *Service) storeCtx(ctx context.Context, id tenant.StoreID) (Identity, context.Context, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if _, _, err := s.d.Directory.Store(ctx, id); err != nil {
		return Identity{}, nil, err
	}
	return who, tenant.WithStore(ctx, id), nil
}

// StoreBilling is what store id is on and owes: its billing page.
func (s *Service) StoreBilling(ctx context.Context, id tenant.StoreID) (billing.Summary, error) {
	if s.d.Billing == nil {
		return billing.Summary{}, errNoBilling
	}
	_, sctx, err := s.storeCtx(ctx, id)
	if err != nil {
		return billing.Summary{}, err
	}
	return s.d.Billing.Summary(sctx)
}

// SettleInvoice records an open invoice of store id's account as paid by
// a transfer the platform confirmed, recorded first in the admin log
// with the reason and the reference.
func (s *Service) SettleInvoice(ctx context.Context, id tenant.StoreID, invoice ulid.ID, rawReference, rawReason string) (billing.Summary, error) {
	if s.d.Billing == nil {
		return billing.Summary{}, errNoBilling
	}
	who, sctx, err := s.storeCtx(ctx, id)
	if err != nil {
		return billing.Summary{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return billing.Summary{}, err
	}
	reference := strings.TrimSpace(rawReference)
	if reference == "" || len(reference) > 120 {
		return billing.Summary{}, errReference
	}
	if err = s.audit(ctx, &who.AdminID, "invoice.settled", "invoice", invoice.String(),
		map[string]any{"reason": reason, "reference": reference, "store": id.String()}); err != nil {
		return billing.Summary{}, err
	}
	if err = s.d.Billing.SettleStoreInvoice(sctx, invoice, reference); err != nil {
		return billing.Summary{}, err
	}
	return s.d.Billing.Summary(sctx)
}
