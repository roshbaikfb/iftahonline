package app

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// An unpaid store's switch to pay as you sell, and the way a pay-as-
// you-sell store pays its fees (ADR-0087, approved 2026-11-04).

var (
	errNotSwitchable = errs.Conflict("not_switchable", "only an unpaid store, before it closes, can switch to pay as you sell")
	errNotPays       = errs.Conflict("not_pays", "the store is not on pay as you sell")
	errNoPaysPlan    = errs.Conflict("no_pays_plan", "pay as you sell is not on sale")
	errNotOffered    = errs.Conflict("collection_not_offered", "this way of paying is not offered")
	errCardRequired  = errs.Conflict("card_required", "save a card first: pay an invoice or top up by card")
	errPrepaidDue    = errs.Conflict("prepaid_pending", "fees taken from the balance wait for the month's invoice; change after it")
)

// SwitchToPays moves the unpaid store in ctx to pay as you sell, paying
// its fees by c, at once: its open invoices are voided (their fees due
// again on the next one), and it is active and open again.
func (s *Service) SwitchToPays(ctx context.Context, c domain.Collection) error {
	info, err := s.storeInfo(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		v, txErr := s.viewTx(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if v.sub.Kind == domain.KindAgency {
			return domain.ErrAgencyPlan
		}
		if !v.has || !v.sub.CanSwitchToPays() {
			return errNotSwitchable
		}
		pays, txErr := paysPlan(ctx, tx)
		if txErr != nil {
			return txErr
		}
		account, b, txErr := s.ownBalanceTx(ctx, tx, info)
		if txErr != nil {
			return txErr
		}
		if txErr = s.allowCollectionTx(ctx, tx, account, b, c); txErr != nil {
			return txErr
		}
		if _, txErr = store.VoidOpenInvoices(ctx, tx, account); txErr != nil {
			return txErr
		}
		if txErr = store.ClearRenewal(ctx, tx); txErr != nil {
			return txErr
		}
		next, txErr := v.sub.SwitchToPays(pays)
		if txErr != nil {
			return txErr
		}
		if txErr = s.saveTx(ctx, tx, next); txErr != nil {
			return txErr
		}
		if txErr = s.hold(ctx, tx, false); txErr != nil {
			return txErr
		}
		return store.SetCollection(ctx, tx, account, c)
	})
}

// SetCollection changes the way the pay-as-you-sell store in ctx pays its
// fees.
func (s *Service) SetCollection(ctx context.Context, c domain.Collection) error {
	info, err := s.storeInfo(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		sub, ok, txErr := store.Subscription(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if !ok || sub.Kind != domain.KindPays || sub.Status != domain.Active {
			return errNotPays
		}
		account, b, txErr := s.ownBalanceTx(ctx, tx, info)
		if txErr != nil {
			return txErr
		}
		if b.Collection == c {
			return nil
		}
		if txErr = s.allowCollectionTx(ctx, tx, account, b, c); txErr != nil {
			return txErr
		}
		return store.SetCollection(ctx, tx, account, c)
	})
}

// allowCollectionTx checks the account may pay by c: away from the
// balance only once its fees are invoiced (their tax is taken from it),
// offered now (or its way already), the card only with one saved.
func (s *Service) allowCollectionTx(ctx context.Context, tx pgx.Tx, account ulid.ID, b store.Balance, c domain.Collection) error {
	if c != domain.CollectBalance && c != domain.CollectCard {
		return errs.Validation(errs.FieldError{Field: "collection", Code: "invalid", Message: "balance or card"})
	}
	if b.Collection == domain.CollectBalance && c != domain.CollectBalance {
		if pending, pErr := store.PrepaidPending(ctx, tx); pErr != nil || pending {
			if pErr != nil {
				return pErr
			}
			return errPrepaidDue
		}
	}
	settings, err := store.CollectionSettings(ctx, tx)
	if err != nil {
		return err
	}
	if b.Collection != c && !slices.Contains(settings.Offered(), c) {
		return errNotOffered
	}
	if c == domain.CollectCard {
		if _, has, mErr := store.PaymentMethod(ctx, tx, account); mErr != nil || !has {
			if mErr != nil {
				return mErr
			}
			return errCardRequired
		}
	}
	return nil
}

// paysPlan is the pay-as-you-sell plan on sale (the first by its order).
func paysPlan(ctx context.Context, tx pgx.Tx) (domain.Plan, error) {
	plans, err := store.Plans(ctx, tx)
	if err != nil {
		return domain.Plan{}, err
	}
	var found *domain.Plan
	for i := range plans {
		p := plans[i].Plan
		if p.Kind == domain.KindPays && p.Active && p.Public && (found == nil || p.Sort < found.Sort) {
			found = &p
		}
	}
	if found == nil {
		return domain.Plan{}, errNoPaysPlan
	}
	return *found, nil
}
