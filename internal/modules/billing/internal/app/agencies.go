package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Agencies set up from the console (ADR-0059): many stores on one
// agency plan, one period and one invoice.

// Agency is an agency account and the plan its stores are on.
type Agency = domain.Agency

var (
	errNoAgency         = errs.NotFound("agency_not_found", "agency not found")
	errAgencyPlan       = errs.Validation(errs.FieldError{Field: "plan_code", Code: "invalid", Message: "an agency plan on sale"})
	errAgencyName       = errs.Validation(errs.FieldError{Field: "name", Code: "invalid", Message: "1 to 120 characters"})
	errAgencyCurrency   = errs.Validation(errs.FieldError{Field: "currency", Code: "invalid", Message: "EGP, SAR or AED"})
	errCurrencyMismatch = errs.Conflict("currency_mismatch", "the store's currency is not the agency's")
	errAlreadyMember    = errs.Conflict("already_member", "the store is in this agency")
	errOpenInvoice      = errs.Conflict("open_invoice", "the store's own account has an open invoice: settle it first")
	errNotInAgency      = errs.Conflict("not_in_agency", "the store is not in an agency")
	errPlanOffSale      = errs.Conflict("plan_off_sale", "the agency's plan is not on sale")
)

// CreateAgency opens an agency account in currency, on the agency plan
// planCode.
func (s *Service) CreateAgency(ctx context.Context, rawName, currency, planCode string) (Agency, error) {
	name := strings.TrimSpace(rawName)
	if n := utf8.RuneCountInString(name); n < 1 || n > 120 {
		return Agency{}, errAgencyName
	}
	if !money.Currency(currency).Valid() {
		return Agency{}, errAgencyCurrency
	}
	a := Agency{Account: domain.Account{ID: s.ids.New(), Kind: domain.AccountAgency, Name: name, Currency: currency,
		Country: domain.CountryOf(currency)}, PlanCode: planCode, CreatedAt: s.now().UTC()}
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		p, ok, err := store.ActivePlan(ctx, tx, planCode)
		if err != nil {
			return err
		}
		if !ok || p.Kind != domain.KindAgency {
			return errAgencyPlan
		}
		if err = store.UseAccount(ctx, tx, a.ID); err != nil {
			return err
		}
		return store.InsertAgency(ctx, tx, a)
	})
	if err != nil {
		return Agency{}, err
	}
	return a, nil
}

// Agencies lists every agency, newest first.
func (s *Service) Agencies(ctx context.Context) (out []Agency, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.Agencies(ctx, tx)
		return txErr
	})
	return out, err
}

// Agency is agency id and its stores.
func (s *Service) Agency(ctx context.Context, id ulid.ID) (a Agency, members []ulid.ID, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := store.UseAccount(ctx, tx, id); txErr != nil {
			return txErr
		}
		var ok bool
		var txErr error
		if a, ok, txErr = store.LockAgency(ctx, tx, id); txErr != nil {
			return txErr
		}
		if !ok {
			return errNoAgency
		}
		members, txErr = store.AccountStores(ctx, tx, id)
		a.Stores = int64(len(members))
		return txErr
	})
	return a, members, err
}

// JoinAgency moves the store in ctx into agency (approved 2026-10-31):
//   - from its own account, if that has no open invoice (its invoices
//     stay there; the next ones are the agency's);
//   - onto the agency's plan version, status and period, shared with its
//     members (the first member's runs to the first of next month);
//   - held or released as the agency is.
//
// The agency is locked, so members join and leave one at a time.
func (s *Service) JoinAgency(ctx context.Context, agency ulid.ID) error {
	if s.stores == nil {
		return errors.New("billing: no stores")
	}
	info, err := s.stores(ctx)
	if err != nil {
		return err
	}
	self, err := tenant.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("billing: %w", err)
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		own, _, err := store.Subscription(ctx, tx)
		if err != nil {
			return err
		}
		old, hasOld, err := store.SubscriptionAccount(ctx, tx)
		if err != nil && !errors.Is(err, store.ErrNoSubscription) {
			return err
		}
		if err = store.UseAccount(ctx, tx, agency); err != nil {
			return err
		}
		a, ok, err := store.LockAgency(ctx, tx, agency)
		switch {
		case err != nil:
			return err
		case !ok:
			return errNoAgency
		case hasOld && old == agency:
			return errAlreadyMember
		case a.Currency != info.Currency:
			return errCurrencyMismatch
		}
		if hasOld {
			if err = s.leaveOwn(ctx, tx, old); err != nil {
				return err
			}
			if err = store.UseAccount(ctx, tx, agency); err != nil {
				return err
			}
		}
		peer, err := s.peer(ctx, tx, agency, self.ID)
		if err != nil {
			return err
		}
		plan := domain.Plan{}
		if peer == nil {
			p, onSale, pErr := store.ActivePlan(ctx, tx, a.PlanCode)
			if pErr != nil {
				return pErr
			}
			if !onSale || p.Kind != domain.KindAgency {
				return errPlanOffSale
			}
			plan = p
		}
		if err = store.JoinAccount(ctx, tx, agency); err != nil {
			return err
		}
		next := domain.JoinAgency(own, plan, peer, s.now())
		return s.putTx(ctx, tx, next, &agency)
	})
}

// leaveOwn takes the store in ctx out of its previous account, which
// must owe nothing; tx is left behind that account's wall.
func (s *Service) leaveOwn(ctx context.Context, tx pgx.Tx, account ulid.ID) error {
	if err := store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	open, err := store.HasOpenInvoice(ctx, tx, account)
	if err != nil {
		return err
	}
	if open {
		return errOpenInvoice
	}
	return store.LeaveAccount(ctx, tx, account)
}

// peer is the subscription of a member of agency other than self (nil
// for the first member), read in that store's wall; tx is back in
// self's after.
func (s *Service) peer(ctx context.Context, tx pgx.Tx, agency, self ulid.ID) (*domain.Subscription, error) {
	members, err := store.AccountStores(ctx, tx, agency)
	if err != nil {
		return nil, err
	}
	var peer *domain.Subscription
	for _, m := range members {
		if m == self {
			continue
		}
		pctx, pErr := store.AsStore(ctx, tx, m)
		if pErr != nil {
			return nil, pErr
		}
		sub, ok, pErr := store.Subscription(pctx, tx)
		if pErr != nil {
			return nil, pErr
		}
		if ok {
			peer = &sub
			break
		}
	}
	if _, err = store.AsStore(ctx, tx, self); err != nil {
		return nil, err
	}
	return peer, nil
}

// LeaveAgency takes the store in ctx out of its agency, onto the free
// plan; its next invoice opens its own account (approved 2026-10-31).
func (s *Service) LeaveAgency(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		own, ok, err := store.Subscription(ctx, tx)
		if err != nil {
			return err
		}
		account, hasAccount, err := store.SubscriptionAccount(ctx, tx)
		if err != nil && !errors.Is(err, store.ErrNoSubscription) {
			return err
		}
		if !ok || !hasAccount {
			return errNotInAgency
		}
		if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		_, isAgency, err := store.LockAgency(ctx, tx, account)
		if err == nil && !isAgency {
			err = errNotInAgency
		}
		if err != nil {
			return err
		}
		if err = store.LeaveAccount(ctx, tx, account); err != nil {
			return err
		}
		free, err := store.FreePlan(ctx, tx)
		if err != nil {
			return err
		}
		return s.putTx(ctx, tx, domain.LeaveAgency(own, free), nil)
	})
}

// putTx writes the store-in-ctx's whole subscription in account, holds or
// releases the store to match, and publishes SubscriptionChanged so
// caches follow (ADR-0050).
func (s *Service) putTx(ctx context.Context, tx pgx.Tx, sub domain.Subscription, account *ulid.ID) error {
	if err := store.PutSubscription(ctx, tx, sub, account); err != nil {
		return err
	}
	if err := s.hold(ctx, tx, sub.Held()); err != nil {
		return err
	}
	meta, err := event.MetaFor(ctx, s.ids, domain.EventSubscriptionChanged, 1)
	if err != nil {
		return err
	}
	if err = s.events.PublishTx(ctx, tx, domain.SubscriptionChanged{Meta: meta, Status: sub.Status}); err != nil {
		return fmt.Errorf("billing: publish: %w", err)
	}
	return nil
}
