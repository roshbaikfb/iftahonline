package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CollectionSettings reads the console's collection settings (ADR-0083).
func CollectionSettings(ctx context.Context, tx Tx) (domain.CollectionSettings, error) {
	r, err := billingdb.New(tx).GetBillingSettings(ctx)
	if err != nil {
		return domain.CollectionSettings{}, fmt.Errorf("billing store: collection settings: %w", err)
	}
	return domain.CollectionSettings{Prepaid: r.PrepaidEnabled, Card: r.CardEnabled,
		Minimums: map[string]int64{"EGP": r.MinimumEgpMinor, "SAR": r.MinimumSarMinor, "AED": r.MinimumAedMinor, "USD": r.MinimumUsdMinor}}, nil
}

// SaveCollectionSettings writes them (the console's role, app_admin).
func SaveCollectionSettings(ctx context.Context, tx Tx, c domain.CollectionSettings) error {
	n, err := billingdb.New(tx).SaveBillingSettings(ctx, billingdb.SaveBillingSettingsParams{
		PrepaidEnabled: c.Prepaid, CardEnabled: c.Card,
		MinimumEgpMinor: c.Minimums["EGP"], MinimumSarMinor: c.Minimums["SAR"], MinimumAedMinor: c.Minimums["AED"],
		MinimumUsdMinor: c.Minimums["USD"]})
	if err != nil {
		return fmt.Errorf("billing store: save collection settings: %w", err)
	}
	if n != 1 {
		return errors.New("billing store: no collection settings row")
	}
	return nil
}

// Balance is an account's prepaid balance.
type Balance struct {
	Kind       domain.AccountKind
	Currency   string
	Minor      int64
	Collection domain.Collection // "" until the account chooses
}

func balanceOf(kind, currency string, minor int64, collection *string) Balance {
	b := Balance{Kind: domain.AccountKind(kind), Currency: currency, Minor: minor}
	if collection != nil {
		b.Collection = domain.Collection(*collection)
	}
	return b
}

// AccountBalance reads the balance of the account tx is behind.
func AccountBalance(ctx context.Context, tx Tx, account ulid.ID) (Balance, error) {
	r, err := billingdb.New(tx).GetAccountBalance(ctx, account)
	if err != nil {
		return Balance{}, fmt.Errorf("billing store: account balance: %w", err)
	}
	return balanceOf(r.Kind, r.Currency, r.BalanceMinor, r.Collection), nil
}

// LockBalance reads the balance locked until tx ends: every change to it
// is made under this lock.
func LockBalance(ctx context.Context, tx Tx, account ulid.ID) (Balance, error) {
	r, err := billingdb.New(tx).LockAccountBalance(ctx, account)
	if err != nil {
		return Balance{}, fmt.Errorf("billing store: lock balance: %w", err)
	}
	return balanceOf(r.Kind, r.Currency, r.BalanceMinor, r.Collection), nil
}

// Entry is one change to a balance.
type Entry struct {
	ID          ulid.ID
	Account     ulid.ID
	Kind        domain.EntryKind
	AmountMinor int64 // signed: a fee is negative
	AfterMinor  int64
	Currency    string
	Reason      domain.CreditReason // a manual entry's
	Reference   string
	Admin       ulid.ID // who added a manual entry
	Order       ulid.ID // a fee's or a refund's order
	Invoice     ulid.ID // an invoice entry's
	Checkout    ulid.ID // a top-up's
	CreatedAt   time.Time
}

// AddEntry writes e and the balance after it, under LockBalance. An
// order's entry of a kind, or an invoice's, is written once: added is
// false for a repeat, and the balance is left as it was.
func AddEntry(ctx context.Context, tx Tx, e Entry) (added bool, err error) {
	q := billingdb.New(tx)
	p := billingdb.InsertBalanceEntryParams{ID: e.ID, AccountID: e.Account, Kind: string(e.Kind), AmountMinor: e.AmountMinor,
		BalanceAfterMinor: e.AfterMinor, Currency: e.Currency, Reference: e.Reference}
	if e.Reason != "" {
		r := string(e.Reason)
		p.Reason = &r
	}
	if !e.Admin.IsZero() {
		p.AdminID = &e.Admin
	}
	if !e.Order.IsZero() {
		p.OrderID = &e.Order
	}
	if !e.Invoice.IsZero() {
		p.InvoiceID = &e.Invoice
	}
	if !e.Checkout.IsZero() {
		p.CheckoutID = &e.Checkout
	}
	n, err := q.InsertBalanceEntry(ctx, p)
	if err != nil {
		return false, fmt.Errorf("billing store: insert balance entry: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if err = q.SetAccountBalance(ctx, billingdb.SetAccountBalanceParams{ID: e.Account, BalanceMinor: e.AfterMinor}); err != nil {
		return false, fmt.Errorf("billing store: set balance: %w", err)
	}
	return true, nil
}

// Entries are the account's latest limit entries, newest first.
func Entries(ctx context.Context, tx Tx, account ulid.ID, limit int32) ([]Entry, error) {
	rows, err := billingdb.New(tx).ListBalanceEntries(ctx, billingdb.ListBalanceEntriesParams{AccountID: account, Lim: limit})
	if err != nil {
		return nil, fmt.Errorf("billing store: balance entries: %w", err)
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		e := Entry{ID: r.ID, Account: account, Kind: domain.EntryKind(r.Kind), AmountMinor: r.AmountMinor, AfterMinor: r.BalanceAfterMinor,
			Currency: r.Currency, Reference: r.Reference, CreatedAt: r.CreatedAt}
		if r.Reason != nil {
			e.Reason = domain.CreditReason(*r.Reason)
		}
		if r.AdminID != nil {
			e.Admin = *r.AdminID
		}
		if r.OrderID != nil {
			e.Order = *r.OrderID
		}
		if r.InvoiceID != nil {
			e.Invoice = *r.InvoiceID
		}
		if r.CheckoutID != nil {
			e.Checkout = *r.CheckoutID
		}
		out[i] = e
	}
	return out, nil
}

// VoidOpenInvoices voids every open invoice of the account tx is behind,
// and makes their fees of the store in ctx due again (its credits
// waiting again): the store moved to pay as you sell (ADR-0087).
func VoidOpenInvoices(ctx context.Context, tx Tx, account ulid.ID) (int, error) {
	ids, err := LockOpenInvoiceIDs(ctx, tx, account)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err = VoidInvoice(ctx, tx, id); err != nil {
			return 0, err
		}
		if err = UnbillInvoice(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// LockOpenInvoiceIDs are the open invoices of the account tx is behind,
// locked until tx ends.
func LockOpenInvoiceIDs(ctx context.Context, tx Tx, account ulid.ID) ([]ulid.ID, error) {
	ids, err := billingdb.New(tx).LockOpenInvoiceIDs(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("billing store: open invoices: %w", err)
	}
	return ids, nil
}

// VoidInvoice voids an open invoice of the account tx is behind.
func VoidInvoice(ctx context.Context, tx Tx, id ulid.ID) error {
	if _, err := billingdb.New(tx).VoidInvoice(ctx, id); err != nil {
		return fmt.Errorf("billing store: void invoice: %w", err)
	}
	return nil
}

// UnbillInvoice makes a voided invoice's fees of the store in ctx due
// again, and its credits waiting again.
func UnbillInvoice(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if _, err = billingdb.New(tx).UnbillFees(ctx, billingdb.UnbillFeesParams{StoreID: store, InvoiceID: &id}); err != nil {
		return fmt.Errorf("billing store: unbill fees: %w", err)
	}
	return nil
}

// SetCollection records the way the account tx is behind pays its fees.
func SetCollection(ctx context.Context, tx Tx, account ulid.ID, c domain.Collection) error {
	v := string(c)
	if err := billingdb.New(tx).SetAccountCollection(ctx, billingdb.SetAccountCollectionParams{ID: account, Collection: &v}); err != nil {
		return fmt.Errorf("billing store: set collection: %w", err)
	}
	return nil
}

// PrepaidPending reports whether the store in ctx has fees taken from its
// balance that no invoice holds yet.
func PrepaidPending(ctx context.Context, tx Tx) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	ok, err := billingdb.New(tx).HasPrepaidDue(ctx, store)
	if err != nil {
		return false, fmt.Errorf("billing store: prepaid pending: %w", err)
	}
	return ok, nil
}
