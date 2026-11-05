package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Topping up the prepaid balance on the gateway's page (ADR-0085).

// maxTopUp bounds one top-up: 1,000,000.00.
const maxTopUp = 100_000_000

// errPrepaidOff refuses a top-up while the platform offers no prepaid
// balance and the account has not chosen one.
var errPrepaidOff = errs.Conflict("prepaid_off", "the prepaid balance is not offered")

// TopUp opens the gateway's page to add amount to the store-in-ctx's
// balance: at least the currency's minimum (ADR-0083). The confirmed
// payment adds it (ProcessCallback).
func (s *Service) TopUp(ctx context.Context, amount int64, payer Payer) (string, error) {
	if !s.canCheckout() {
		return "", errCheckoutOff
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return "", err
	}
	info, err := s.storeInfo(ctx)
	if err != nil {
		return "", err
	}
	c := store.Checkout{ID: s.ids.New(), AmountMinor: amount}
	c.Reference = "bc_" + c.ID.String()
	var gw CheckoutGateway
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		account, b, txErr := s.ownBalanceTx(ctx, tx, info)
		if txErr != nil {
			return txErr
		}
		settings, txErr := store.CollectionSettings(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if !settings.Prepaid && b.Collection != domain.CollectBalance {
			return errPrepaidOff
		}
		if minimum := settings.Minimums[b.Currency]; amount < minimum || amount > maxTopUp {
			return errs.Validation(errs.FieldError{Field: "amount_minor", Code: "out_of_range",
				Message: fmt.Sprintf("between %d and %d", minimum, maxTopUp)})
		}
		if c.Gateway, gw, txErr = s.checkoutForTx(ctx, tx, account); txErr != nil {
			return txErr
		}
		c.Account, c.Currency = account, b.Currency
		return store.InsertCheckout(ctx, tx, c)
	})
	if err != nil {
		return "", err
	}
	return s.openPage(ctx, gw, c, "شحن الرصيد", payer, "/stores/"+storeID.String()+"/billing/balance")
}

// toppedUp adds a confirmed top-up c (gateway transaction txn) to its
// balance, for the store in ctx, in the transaction that marks it paid:
// a repeated callback finds it paid and adds nothing.
func (s *Service) toppedUp(ctx context.Context, receipt ulid.ID, c store.Checkout, txn string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		marked, err := store.MarkCheckoutPaid(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		outcome := "duplicate"
		if marked {
			outcome = "topped_up"
			if err = s.topUpTx(ctx, tx, c, txn); err != nil {
				return err
			}
		}
		_, err = s.inbox.MarkProcessedTx(ctx, tx, receipt, outcome)
		return err
	})
}

func (s *Service) topUpTx(ctx context.Context, tx pgx.Tx, c store.Checkout, txn string) error {
	if err := store.UseAccount(ctx, tx, c.Account); err != nil {
		return err
	}
	b, err := store.LockBalance(ctx, tx, c.Account)
	if err != nil {
		return err
	}
	if _, err = s.moveBalanceTx(ctx, tx, c.Account, b, store.Entry{Kind: domain.EntryTopUp, AmountMinor: c.AmountMinor, Reference: txn,
		Checkout: c.ID}); err != nil {
		return err
	}
	return s.notifyTx(ctx, tx, Notice{Kind: domain.NoticeToppedUp, TotalMinor: c.AmountMinor, Currency: c.Currency})
}
