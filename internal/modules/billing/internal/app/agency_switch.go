package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's switch of an unpaid agency's stores to pay as you sell
// (ADR-0087 §7, approved 2026-11-04 with the mockup): the platform's
// staff agree it with the agency, for all of its stores or some.

var (
	errAgencyNotUnpaid = errs.Conflict("agency_not_unpaid", "the agency owes no invoice")
	errAgencyNoCard    = errs.Conflict("card_required", "the agency has no card saved")
)

// AgencyOpenInvoices are the agency's open invoices, newest first.
func (s *Service) AgencyOpenInvoices(ctx context.Context, agency ulid.ID) ([]store.OpenInvoiceRow, error) {
	var out []store.OpenInvoiceRow
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.UseAccount(ctx, tx, agency); err != nil {
			return err
		}
		if _, ok, err := store.LockAgency(ctx, tx, agency); err != nil || !ok {
			if err == nil {
				err = errNoAgency
			}
			return err
		}
		var err error
		out, err = store.OpenInvoices(ctx, tx, agency)
		return err
	})
	return out, err
}

// SwitchAgencyStores moves stores of the unpaid agency to pay as you
// sell, each with its own account paying its fees by c (the card is the
// agency's, copied). In one transaction the agency's open invoices are
// voided and their fees due again; the stores that stay get the month's
// invoice again, at the agency's price, their grace going on.
func (s *Service) SwitchAgencyStores(ctx context.Context, agency ulid.ID, stores []ulid.ID, c domain.Collection) error {
	if c != domain.CollectBalance && c != domain.CollectCard {
		return errs.Validation(errs.FieldError{Field: "collection", Code: "invalid", Message: "balance or card"})
	}
	if s.stores == nil {
		return errors.New("billing: no stores")
	}
	if len(stores) == 0 {
		return errs.Validation(errs.FieldError{Field: "store_ids", Code: "required", Message: "at least one store"})
	}
	var members []ulid.ID
	if err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.UseAccount(ctx, tx, agency); err != nil {
			return err
		}
		if _, ok, err := store.LockAgency(ctx, tx, agency); err != nil || !ok {
			if err == nil {
				err = errNoAgency
			}
			return err
		}
		var err error
		members, err = store.AccountStores(ctx, tx, agency)
		return err
	}); err != nil {
		return err
	}
	for _, id := range stores {
		if !slices.Contains(members, id) {
			return errs.Validation(errs.FieldError{Field: "store_ids", Code: "not_member", Message: "a store of another account"})
		}
	}
	// Names are read before the transaction, which holds one connection.
	all := make([]member, len(members))
	for i, id := range members {
		info, err := s.stores(tenant.WithStore(ctx, tenant.StoreID{ID: id}))
		if err != nil {
			return err
		}
		all[i] = member{id: id, name: info.Name}
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return s.switchAgencyTx(ctx, tx, agency, stores, all, c) })
}

func (s *Service) switchAgencyTx(ctx context.Context, tx pgx.Tx, agency ulid.ID, moving []ulid.ID, all []member, c domain.Collection) error {
	if err := store.UseAccount(ctx, tx, agency); err != nil {
		return err
	}
	a, ok, err := store.LockAgency(ctx, tx, agency)
	if err != nil {
		return err
	}
	if !ok {
		return errNoAgency
	}
	open, err := store.OpenInvoices(ctx, tx, agency)
	if err != nil {
		return err
	}
	if len(open) == 0 {
		return errAgencyNotUnpaid
	}
	var token []byte
	var card store.Method
	if c == domain.CollectCard {
		var has bool
		if card, has, err = store.PaymentMethod(ctx, tx, agency); err != nil {
			return err
		}
		if !has || s.sealer == nil {
			return errAgencyNoCard
		}
		if token, err = s.sealer.Open(ctx, card.Token, methodAAD(agency)); err != nil {
			return fmt.Errorf("billing: open the agency's card: %w", err)
		}
	}
	pays, err := paysPlan(ctx, tx)
	if err != nil {
		return err
	}
	ids, err := store.LockOpenInvoiceIDs(ctx, tx, agency)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = store.VoidInvoice(ctx, tx, id); err != nil {
			return err
		}
	}
	var staying []member
	for _, m := range all {
		sctx, sErr := store.AsStore(ctx, tx, m.id)
		if sErr != nil {
			return sErr
		}
		for _, id := range ids {
			if sErr = store.UnbillInvoice(sctx, tx, id); sErr != nil {
				return sErr
			}
		}
		if sErr = store.ClearRenewal(sctx, tx); sErr != nil {
			return sErr
		}
		if !slices.Contains(moving, m.id) {
			staying = append(staying, m)
			continue
		}
		if sErr = s.agencyStoreToPaysTx(sctx, tx, a, m, pays, c, card, token); sErr != nil {
			return sErr
		}
	}
	if len(staying) == 0 {
		return nil
	}
	// The stores that stay: the month's invoice again, at the agency's price.
	return s.invoiceTx(ctx, tx, agency, open[len(open)-1].Period, staying)
}

// agencyStoreToPaysTx moves the store in ctx out of agency a onto pays,
// with its own account paying by c.
func (s *Service) agencyStoreToPaysTx(ctx context.Context, tx pgx.Tx, a domain.Agency, m member, pays domain.Plan, c domain.Collection,
	card store.Method, token []byte) error {
	own, ok, err := store.Subscription(ctx, tx)
	if err != nil {
		return err
	}
	if !ok {
		return errNotInAgency
	}
	next, err := domain.AgencyToPays(own, pays)
	if err != nil {
		return err
	}
	// Behind the agency's wall again: the previous store opened its own.
	if err = store.UseAccount(ctx, tx, a.ID); err != nil {
		return err
	}
	if err = store.LeaveAccount(ctx, tx, a.ID); err != nil {
		return err
	}
	// On pays with no account (it leaves the agency's), then its own.
	if err = s.putTx(ctx, tx, next, nil); err != nil {
		return err
	}
	account := s.ids.New()
	if err = store.OpenStoreAccount(ctx, tx, domain.Account{ID: account, Kind: domain.AccountStore, Name: m.name, Currency: a.Currency,
		Country: a.Country}); err != nil {
		return err
	}
	if err = store.SetCollection(ctx, tx, account, c); err != nil {
		return err
	}
	if c == domain.CollectCard {
		sealed, sErr := s.sealer.Seal(ctx, token, methodAAD(account))
		if sErr != nil {
			return fmt.Errorf("billing: seal the store's card: %w", sErr)
		}
		if err = store.SavePaymentMethod(ctx, tx, account, store.Method{Gateway: card.Gateway, Token: sealed, Label: card.Label}); err != nil {
			return err
		}
	}
	return nil
}
