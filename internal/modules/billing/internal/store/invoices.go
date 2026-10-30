package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNoSubscription means the store has no subscription row to attach
// to an account.
var ErrNoSubscription = errors.New("billing store: the store has no subscription")

// UseAccount puts tx behind account's wall (ADR-0049). The caller has
// the account from the store's own subscription, or a system job's
// args: never from a request.
func UseAccount(ctx context.Context, tx Tx, account ulid.ID) error {
	if err := billingdb.New(tx).UseAccount(ctx, account); err != nil {
		return fmt.Errorf("billing store: use account: %w", err)
	}
	return nil
}

// AsStore moves tx to store's wall and returns its context, for an
// agency's stores in one invoice's transaction.
func AsStore(ctx context.Context, tx Tx, store ulid.ID) (context.Context, error) {
	if err := billingdb.New(tx).UseStore(ctx, store); err != nil {
		return nil, fmt.Errorf("billing store: use store: %w", err)
	}
	return tenant.WithStore(ctx, tenant.StoreID{ID: store}), nil
}

// BillableStores are the stores with something to bill for period:
// renewals due by its end, its due fees, or credits (app.billable_stores).
func BillableStores(ctx context.Context, tx Tx, period time.Time) ([]ulid.ID, error) {
	ids, err := billingdb.New(tx).BillableStores(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("billing store: billable stores: %w", err)
	}
	return ids, nil
}

// SubscriptionAccount is the account of the store in ctx; ok is false
// when it has none yet.
func SubscriptionAccount(ctx context.Context, tx Tx) (id ulid.ID, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return id, false, err
	}
	acc, err := billingdb.New(tx).GetSubscriptionAccount(ctx, store)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return id, false, ErrNoSubscription
	case err != nil:
		return id, false, fmt.Errorf("billing store: subscription account: %w", err)
	case acc == nil:
		return id, false, nil
	}
	return *acc, true, nil
}

// OpenStoreAccount creates the store-in-ctx's own account and puts tx
// behind both walls; the subscription points at it.
func OpenStoreAccount(ctx context.Context, tx Tx, a domain.Account) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = UseAccount(ctx, tx, a.ID); err != nil {
		return err
	}
	q := billingdb.New(tx)
	if err = q.InsertAccount(ctx, billingdb.InsertAccountParams{ID: a.ID, Kind: string(a.Kind), Name: a.Name, Currency: a.Currency, Country: a.Country}); err != nil {
		return fmt.Errorf("billing store: insert account: %w", err)
	}
	if err = q.InsertAccountStore(ctx, billingdb.InsertAccountStoreParams{AccountID: a.ID, MemberStoreID: store}); err != nil {
		return fmt.Errorf("billing store: insert account store: %w", err)
	}
	n, err := q.SetSubscriptionAccount(ctx, billingdb.SetSubscriptionAccountParams{StoreID: store, AccountID: &a.ID})
	if err != nil {
		return fmt.Errorf("billing store: set account: %w", err)
	}
	if n != 1 {
		return ErrNoSubscription
	}
	return nil
}

// Account reads the account tx is behind.
func Account(ctx context.Context, tx Tx, id ulid.ID) (domain.Account, error) {
	r, err := billingdb.New(tx).GetAccount(ctx, id)
	if err != nil {
		return domain.Account{}, fmt.Errorf("billing store: get account: %w", err)
	}
	return domain.Account{ID: r.ID, Kind: domain.AccountKind(r.Kind), Name: r.Name, Currency: r.Currency, Country: r.Country}, nil
}

// AccountStores are the stores an account pays for.
func AccountStores(ctx context.Context, tx Tx, account ulid.ID) ([]ulid.ID, error) {
	ids, err := billingdb.New(tx).ListAccountStores(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("billing store: account stores: %w", err)
	}
	return ids, nil
}

// InvoiceExists reports whether the account has period's invoice.
func InvoiceExists(ctx context.Context, tx Tx, account ulid.ID, period time.Time) (bool, error) {
	ok, err := billingdb.New(tx).InvoiceExists(ctx, billingdb.InvoiceExistsParams{AccountID: account, Period: period})
	if err != nil {
		return false, fmt.Errorf("billing store: invoice exists: %w", err)
	}
	return ok, nil
}

// TaxRate is a country's VAT in basis points.
func TaxRate(ctx context.Context, tx Tx, country string) (int32, error) {
	r, err := billingdb.New(tx).GetTaxRate(ctx, country)
	if err != nil {
		return 0, fmt.Errorf("billing store: tax rate %s: %w", country, err)
	}
	return r, nil
}

// Renewal is a paid period due to be billed.
type Renewal struct {
	Kind        domain.Kind
	PlanName    string
	Cycle       domain.Cycle
	AmountMinor int64
}

// RenewalDue is the store-in-ctx's renewal ending by renewsBy, not
// billed yet; ok is false when there is none.
func RenewalDue(ctx context.Context, tx Tx, currency string, renewsBy time.Time) (r Renewal, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return r, false, err
	}
	row, err := billingdb.New(tx).RenewalDue(ctx, billingdb.RenewalDueParams{StoreID: store, Currency: currency, RenewsBy: &renewsBy})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return r, false, nil
	case err != nil:
		return r, false, fmt.Errorf("billing store: renewal due: %w", err)
	}
	if r.Kind, err = domain.ParseKind(row.Kind); err != nil {
		return r, false, err
	}
	if row.Cycle != nil {
		if r.Cycle, err = domain.ParseCycle(*row.Cycle); err != nil {
			return r, false, err
		}
	}
	r.PlanName, r.AmountMinor = row.NameAr, row.AmountMinor
	return r, true, nil
}

// FeeSums are a store's fees to bill: due in period, and credits.
type FeeSums struct {
	Due, DueMinor         int64
	Credits, CreditsMinor int64
}

// SumFees reads the store-in-ctx's fees to bill.
func SumFees(ctx context.Context, tx Tx, period time.Time, currency string) (FeeSums, error) {
	store, err := current(ctx)
	if err != nil {
		return FeeSums{}, err
	}
	q := billingdb.New(tx)
	due, err := q.SumDueFees(ctx, billingdb.SumDueFeesParams{StoreID: store, Period: period, Currency: currency})
	if err != nil {
		return FeeSums{}, fmt.Errorf("billing store: due fees: %w", err)
	}
	cr, err := q.SumCreditFees(ctx, billingdb.SumCreditFeesParams{StoreID: store, Currency: currency})
	if err != nil {
		return FeeSums{}, fmt.Errorf("billing store: credits: %w", err)
	}
	return FeeSums{Due: due.N, DueMinor: due.Total, Credits: cr.N, CreditsMinor: cr.Total}, nil
}

// Invoice is an invoice to write.
type Invoice struct {
	ID       ulid.ID
	Account  ulid.ID
	Period   time.Time
	Currency string
	Totals   domain.InvoiceTotals
	Issued   time.Time
	Lines    []domain.Line
}

// InsertInvoice numbers and writes an invoice with its lines, in the
// account's wall, and returns its number.
func InsertInvoice(ctx context.Context, tx Tx, inv Invoice) (string, error) {
	q := billingdb.New(tx)
	n, err := q.NextInvoiceNumber(ctx, int32(inv.Issued.UTC().Year())) //nolint:gosec // a year fits
	if err != nil {
		return "", fmt.Errorf("billing store: invoice number: %w", err)
	}
	number := domain.InvoiceNumber(inv.Issued.UTC().Year(), n)
	if err = q.InsertInvoice(ctx, billingdb.InsertInvoiceParams{
		ID: inv.ID, AccountID: inv.Account, Number: number, Period: inv.Period, Currency: inv.Currency,
		SubtotalMinor: inv.Totals.SubtotalMinor, TaxBps: inv.Totals.TaxBps, TaxMinor: inv.Totals.TaxMinor, TotalMinor: inv.Totals.TotalMinor,
		IssuedAt: inv.Issued, DueAt: domain.DueAt(inv.Issued),
	}); err != nil {
		return "", fmt.Errorf("billing store: insert invoice: %w", err)
	}
	for i, l := range inv.Lines {
		var sid *ulid.ID
		if !l.StoreID.IsZero() {
			sid = &l.StoreID
		}
		if err = q.InsertInvoiceLine(ctx, billingdb.InsertInvoiceLineParams{
			InvoiceID: inv.ID, AccountID: inv.Account, LineNo: int32(i + 1), Kind: string(l.Kind), //nolint:gosec // a few lines
			LineStoreID: sid, Description: l.Description, Quantity: l.Quantity, AmountMinor: l.AmountMinor,
		}); err != nil {
			return "", fmt.Errorf("billing store: insert invoice line: %w", err)
		}
	}
	return number, nil
}

// MarkBilled records, in the store-in-ctx's wall, what invoice billed:
// its due fees of period, its credits, and its renewal when renewed.
func MarkBilled(ctx context.Context, tx Tx, invoice ulid.ID, period time.Time, currency string, renewed bool) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := billingdb.New(tx)
	if _, err = q.MarkFeesInvoiced(ctx, billingdb.MarkFeesInvoicedParams{StoreID: store, Period: period, Currency: currency, InvoiceID: &invoice}); err != nil {
		return fmt.Errorf("billing store: mark fees: %w", err)
	}
	if _, err = q.MarkCreditsUsed(ctx, billingdb.MarkCreditsUsedParams{StoreID: store, Currency: currency, InvoiceID: &invoice}); err != nil {
		return fmt.Errorf("billing store: mark credits: %w", err)
	}
	if renewed {
		if err = q.MarkRenewalBilled(ctx, billingdb.MarkRenewalBilledParams{StoreID: store, InvoiceID: &invoice}); err != nil {
			return fmt.Errorf("billing store: mark renewal: %w", err)
		}
	}
	return nil
}
