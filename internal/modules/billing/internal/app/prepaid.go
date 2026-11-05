package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Paying pay-as-you-sell fees from the prepaid balance (ADR-0084,
// approved 2026-11-05): a delivered order's fee is taken from the
// balance, its return puts it back at once, and the month's invoice is
// paid from it, giving only its tax.

// prepayTx takes the fee of order, just recorded for the store in ctx,
// from the store's balance when its account pays that way.
func (s *Service) prepayTx(ctx context.Context, tx pgx.Tx, order ulid.ID, fee int64, currency string) error {
	if fee <= 0 {
		return nil
	}
	account, has, err := accountOf(ctx, tx)
	if err != nil || !has {
		return err
	}
	if err = store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	b, err := store.LockBalance(ctx, tx, account)
	if err != nil || b.Collection != domain.CollectBalance {
		return err
	}
	if b.Currency != currency {
		return errCurrency(currency, b.Currency)
	}
	added, err := s.moveBalanceTx(ctx, tx, account, b, store.Entry{Kind: domain.EntryFee, AmountMinor: -fee, Order: order})
	if err != nil || !added {
		return err
	}
	return store.MarkSellFeePrepaid(ctx, tx, order)
}

// refundTx puts the fee of a returned order back in the store-in-ctx's
// balance, once, when it was taken from it: before or after its invoice.
func (s *Service) refundTx(ctx context.Context, tx pgx.Tx, f domain.SellFee) error {
	if !f.Prepaid || f.FeeMinor <= 0 {
		return nil
	}
	account, has, err := accountOf(ctx, tx)
	if err != nil || !has {
		return err
	}
	if err = store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	b, err := store.LockBalance(ctx, tx, account)
	if err != nil {
		return err
	}
	_, err = s.moveBalanceTx(ctx, tx, account, b, store.Entry{Kind: domain.EntryFeeRefund, AmountMinor: f.FeeMinor, Order: f.OrderID})
	return err
}

// payFromBalanceTx pays a store account's new invoice from its balance:
// the invoice is the tax document of fees the balance already gave, so
// the balance gives only the rest (its tax). An invoice with anything
// but fees and their credits is left to the card (it has no such lines
// while the store sells as it goes).
func (s *Service) payFromBalanceTx(ctx context.Context, tx pgx.Tx, acct domain.Account, inv store.Invoice, number string, m member) (bool, error) {
	if acct.Kind != domain.AccountStore {
		return false, nil
	}
	for _, l := range inv.Lines {
		if l.Kind != domain.LineSellFees && l.Kind != domain.LineFeeCredit {
			return false, nil
		}
	}
	b, err := store.LockBalance(ctx, tx, acct.ID)
	if err != nil || b.Collection != domain.CollectBalance {
		return false, err
	}
	sctx, err := store.AsStore(ctx, tx, m.id)
	if err != nil {
		return false, err
	}
	settled, err := store.PrepaidOnInvoice(sctx, tx, inv.ID)
	if err != nil {
		return false, err
	}
	if _, err = store.MarkInvoicePaid(ctx, tx, inv.ID, s.now().UTC()); err != nil {
		return false, err
	}
	if err = store.InsertAttempt(ctx, tx, store.Attempt{ID: s.ids.New(), Invoice: inv.ID, Account: acct.ID, Gateway: "balance",
		Outcome: "manual", Reference: "balance", AmountMinor: inv.Totals.TotalMinor}); err != nil {
		return false, err
	}
	if due := inv.Totals.TotalMinor - settled; due != 0 {
		if _, err = s.moveBalanceTx(sctx, tx, acct.ID, b, store.Entry{Kind: domain.EntryInvoice, AmountMinor: -due, Invoice: inv.ID, Reference: number}); err != nil {
			return false, err
		}
	}
	return true, s.notifyTx(sctx, tx, Notice{Kind: domain.NoticePaid, Invoice: number, TotalMinor: inv.Totals.TotalMinor, Currency: inv.Currency})
}

// moveBalanceTx writes e to account's balance b (locked by the caller),
// for the store in ctx, then follows what the move changes: a balance
// that runs out puts the store past due, one that comes back restores it
// (when it owes no invoice), one that falls under half the minimum is
// told. added is false for a repeated order's or invoice's entry.
func (s *Service) moveBalanceTx(ctx context.Context, tx pgx.Tx, account ulid.ID, b store.Balance, e store.Entry) (bool, error) {
	e.ID, e.Account, e.Currency, e.AfterMinor = s.ids.New(), account, b.Currency, b.Minor+e.AmountMinor
	added, err := store.AddEntry(ctx, tx, e)
	if err != nil || !added {
		return added, err
	}
	settings, err := store.CollectionSettings(ctx, tx)
	if err != nil {
		return true, err
	}
	return true, s.followBalanceTx(ctx, tx, account, b.Minor, e.AfterMinor, settings.Minimums[b.Currency], b.Currency)
}

func (s *Service) followBalanceTx(ctx context.Context, tx pgx.Tx, account ulid.ID, before, after, minimum int64, currency string) error {
	sub, ok, err := store.Subscription(ctx, tx)
	if err != nil || !ok {
		return err
	}
	switch {
	case after <= 0 && sub.Status == domain.Active && sub.Kind.Billed():
		pd, pErr := sub.PaymentFailed(s.now().UTC(), s.policy)
		if pErr != nil {
			return pErr
		}
		if err = s.saveTx(ctx, tx, pd); err != nil {
			return err
		}
		return s.notifyTx(ctx, tx, Notice{Kind: domain.NoticeBalanceEmpty, TotalMinor: -after, Currency: currency,
			GraceUntil: pd.GraceUntil, ClosesAt: pd.ClosesAt(s.policy)})
	case after > 0 && before <= 0 && (sub.Status == domain.PastDue || sub.Status == domain.Suspended || sub.Status == domain.Cancelled):
		owes, oErr := store.HasOpenInvoice(ctx, tx, account)
		if oErr != nil || owes {
			return oErr
		}
		next, _, sErr := sub.Settle(false)
		if sErr != nil {
			return sErr
		}
		if err = s.saveTx(ctx, tx, next); err != nil {
			return err
		}
		if err = s.hold(ctx, tx, false); err != nil {
			return err
		}
		return s.notifyTx(ctx, tx, Notice{Kind: domain.NoticeBalanceRestored, TotalMinor: after, Currency: currency})
	case after > 0 && after < minimum/2 && before >= minimum/2:
		return s.notifyTx(ctx, tx, Notice{Kind: domain.NoticeBalanceLow, TotalMinor: after, Currency: currency})
	}
	return nil
}

// errCurrency is a fee in another currency than its account's: a store's
// currency is fixed, so it is a bug, not an input.
func errCurrency(fee, account string) error {
	return fmt.Errorf("billing: a %s fee for a %s account", fee, account)
}
