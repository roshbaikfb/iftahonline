package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Collecting pay-as-you-sell fees (ADR-0083): the console's settings,
// and each account's prepaid balance.

// balanceEntries is how many entries a balance shows.
const balanceEntries = 50

// errAgencyBalance refuses a balance for an agency's store: an agency
// pays one invoice for its stores (approved 2026-11-04).
var errAgencyBalance = errs.Conflict("agency_managed", "an agency's stores have no balance of their own")

// errNoBilling refuses a credit to a store that never had a plan row
// (the free plan by default): it has no account to hold a balance.
var errNoBilling = errs.Conflict("no_billing", "the store has no billing yet")

// CollectionSettings are the console's collection settings.
func (s *Service) CollectionSettings(ctx context.Context) (domain.CollectionSettings, error) {
	var c domain.CollectionSettings
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = store.CollectionSettings(ctx, tx)
		return txErr
	})
	return c, err
}

// SaveCollectionSettings checks and writes them (on the console's pool).
// Turning a way off reaches only accounts that choose after it.
func (s *Service) SaveCollectionSettings(ctx context.Context, c domain.CollectionSettings) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return store.SaveCollectionSettings(ctx, tx, c) })
}

// BalanceEntry is one change to a balance.
type BalanceEntry = store.Entry

// Balance is a store's account balance as the console shows it.
type Balance struct {
	Currency     string
	Minor        int64
	MinimumMinor int64 // the currency's minimum (ADR-0083)
	Level        domain.BalanceLevel
	Collection   domain.Collection // "" until the account chooses
	Entries      []BalanceEntry    // the latest, newest first
}

// StoreBalance is the balance of the store-in-ctx's own account; a store
// never invoiced has none yet, so 0 in its currency.
func (s *Service) StoreBalance(ctx context.Context) (Balance, error) {
	var out Balance
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		account, has, err := accountOf(ctx, tx)
		if err != nil {
			return err
		}
		settings, err := store.CollectionSettings(ctx, tx)
		if err != nil {
			return err
		}
		if !has {
			if s.stores == nil {
				return errors.New("billing: no stores")
			}
			info, iErr := s.stores(ctx)
			if iErr != nil {
				return iErr
			}
			out = Balance{Currency: info.Currency, MinimumMinor: settings.Minimums[info.Currency]}
			out.Level = domain.LevelOf(0, out.MinimumMinor)
			return nil
		}
		return s.balanceTx(ctx, tx, account, settings, &out)
	})
	return out, err
}

func (s *Service) balanceTx(ctx context.Context, tx pgx.Tx, account ulid.ID, settings domain.CollectionSettings, out *Balance) error {
	if err := store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	b, err := store.AccountBalance(ctx, tx, account)
	if err != nil {
		return err
	}
	if b.Kind == domain.AccountAgency {
		return errAgencyBalance
	}
	entries, err := store.Entries(ctx, tx, account, balanceEntries)
	if err != nil {
		return err
	}
	minimum := settings.Minimums[b.Currency]
	*out = Balance{Currency: b.Currency, Minor: b.Minor, MinimumMinor: minimum, Level: domain.LevelOf(b.Minor, minimum), Collection: b.Collection,
		Entries: entries}
	return nil
}

// CreditBalance adds c to the store-in-ctx's own balance, by the console
// (admin), opening the store's account when it has none. Whatever the
// balance owes is covered first: the entry adds to it as it stands.
func (s *Service) CreditBalance(ctx context.Context, c domain.Credit, admin ulid.ID) (Balance, error) {
	if admin.IsZero() {
		return Balance{}, errors.New("billing: a manual credit needs its admin")
	}
	var info StoreInfo
	if s.stores != nil {
		var err error
		if info, err = s.stores(ctx); err != nil {
			return Balance{}, err
		}
	}
	var out Balance
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		account, has, err := store.SubscriptionAccount(ctx, tx)
		if errors.Is(err, store.ErrNoSubscription) {
			return errNoBilling
		}
		if err != nil {
			return err
		}
		if !has {
			if info.Currency == "" {
				return errors.New("billing: no stores")
			}
			account = s.ids.New()
			if err = store.OpenStoreAccount(ctx, tx, domain.Account{ID: account, Kind: domain.AccountStore, Name: info.Name,
				Currency: info.Currency, Country: domain.CountryOf(info.Currency)}); err != nil {
				return err
			}
		} else if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		b, err := store.LockBalance(ctx, tx, account)
		if err != nil {
			return err
		}
		if b.Kind == domain.AccountAgency {
			return errAgencyBalance
		}
		if err = store.AddEntry(ctx, tx, store.Entry{ID: s.ids.New(), Account: account, Kind: domain.EntryManual, AmountMinor: c.AmountMinor,
			AfterMinor: b.Minor + c.AmountMinor, Currency: b.Currency, Reason: c.Reason, Reference: c.Reference, Admin: admin}); err != nil {
			return err
		}
		settings, err := store.CollectionSettings(ctx, tx)
		if err != nil {
			return err
		}
		return s.balanceTx(ctx, tx, account, settings, &out)
	})
	return out, err
}
