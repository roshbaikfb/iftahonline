package app

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// The card with a minimum (ADR-0086, approved 2026-11-04): an account
// that pays its fees by card is invoiced as soon as its due fees reach
// the currency's minimum, or with the month's invoice, whichever comes
// first. The dunning charges the invoice within the hour, as any other.

// thresholdTx writes a fees invoice for the store in ctx, in the
// transaction that recorded a fee, when its account pays by card and
// its due fees (before tax) reached the minimum. The account's row lock
// serializes it with the month's invoice and with other deliveries, so
// a fee is never invoiced twice.
func (s *Service) thresholdTx(ctx context.Context, tx pgx.Tx, currency string) error {
	account, has, err := accountOf(ctx, tx)
	if err != nil || !has {
		return err
	}
	if err = store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	b, err := store.LockBalance(ctx, tx, account)
	if err != nil || b.Collection != domain.CollectCard || b.Kind != domain.AccountStore {
		return err
	}
	if b.Currency != currency {
		return errCurrency(currency, b.Currency)
	}
	settings, err := store.CollectionSettings(ctx, tx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	period := domain.PeriodOf(now)
	sums, err := store.SumFees(ctx, tx, period, currency)
	if err != nil || sums.DueMinor < settings.Minimums[currency] {
		return err
	}
	acct, err := store.Account(ctx, tx, account)
	if err != nil {
		return err
	}
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	tax, err := store.TaxRate(ctx, tx, acct.Country)
	if err != nil {
		return err
	}
	lines := feeLines(sums, id.ID, acct.Name, period)
	totals, ok := domain.Totals(lines, tax)
	if !ok {
		return nil // credits as large as the fees: they wait
	}
	inv := store.Invoice{ID: s.ids.New(), Account: account, Period: period, Currency: currency, Totals: totals, Issued: now, Lines: lines, Fees: true}
	if _, err = store.InsertInvoice(ctx, tx, inv); err != nil {
		return err
	}
	return store.MarkBilled(ctx, tx, inv.ID, period, currency, nil)
}
