package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Checkout is a "pay now" on the gateway's page.
type Checkout struct {
	ID          ulid.ID
	Account     ulid.ID
	Invoice     ulid.ID
	Reference   string
	Currency    string
	AmountMinor int64
	Status      string // open, paid, failed
}

// InsertCheckout records a checkout of the store in ctx.
func InsertCheckout(ctx context.Context, tx Tx, c Checkout) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = billingdb.New(tx).InsertCheckout(ctx, billingdb.InsertCheckoutParams{StoreID: store, ID: c.ID, AccountID: c.Account,
		InvoiceID: c.Invoice, Reference: c.Reference, Currency: c.Currency, AmountMinor: c.AmountMinor}); err != nil {
		return fmt.Errorf("billing store: insert checkout: %w", err)
	}
	return nil
}

// SetCheckoutOrder records the gateway's order of the checkout with
// reference, once.
func SetCheckoutOrder(ctx context.Context, tx Tx, reference, order string) error {
	store, err := current(ctx)
	if err != nil || order == "" {
		return err
	}
	if err = billingdb.New(tx).SetCheckoutOrder(ctx, billingdb.SetCheckoutOrderParams{StoreID: store, Reference: reference, PaymobOrderID: &order}); err != nil {
		return fmt.Errorf("billing store: checkout order: %w", err)
	}
	return nil
}

// FindCheckout is the store-in-ctx's checkout by our reference or the
// gateway's order; ok is false when there is none.
func FindCheckout(ctx context.Context, tx Tx, reference, order string) (Checkout, bool, error) {
	store, err := current(ctx)
	if err != nil {
		return Checkout{}, false, err
	}
	r, err := billingdb.New(tx).GetCheckout(ctx, billingdb.GetCheckoutParams{StoreID: store, Reference: reference, PaymobOrderID: order})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Checkout{}, false, nil
	case err != nil:
		return Checkout{}, false, fmt.Errorf("billing store: get checkout: %w", err)
	}
	return Checkout{ID: r.ID, Account: r.AccountID, Invoice: r.InvoiceID, Reference: r.Reference, Currency: r.Currency,
		AmountMinor: r.AmountMinor, Status: r.Status}, true, nil
}

// SetCheckoutStatus records a checkout's outcome.
func SetCheckoutStatus(ctx context.Context, tx Tx, id ulid.ID, status string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = billingdb.New(tx).SetCheckoutStatus(ctx, billingdb.SetCheckoutStatusParams{StoreID: store, ID: id, Status: status}); err != nil {
		return fmt.Errorf("billing store: checkout status: %w", err)
	}
	return nil
}

// CheckoutStore places a callback across stores (app.billing_checkout_
// store); ok is false when no checkout matches.
func CheckoutStore(ctx context.Context, tx Tx, reference, order string) (ulid.ID, bool, error) {
	id, err := billingdb.New(tx).CheckoutStore(ctx, billingdb.CheckoutStoreParams{Reference: reference, PaymobOrderID: order})
	if err != nil {
		return ulid.ID{}, false, fmt.Errorf("billing store: checkout store: %w", err)
	}
	return id, !id.IsZero(), nil
}

// OpenInvoiceRow is an open invoice as the merchant sees it.
type OpenInvoiceRow struct {
	ID          ulid.ID
	Number      string
	Period      time.Time
	Currency    string
	TotalMinor  int64
	DueAt       time.Time
	Attempts    int
	NextAttempt time.Time
}

// OpenInvoices lists the open invoices of the account tx is behind.
func OpenInvoices(ctx context.Context, tx Tx, account ulid.ID) ([]OpenInvoiceRow, error) {
	rows, err := billingdb.New(tx).ListOpenInvoices(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("billing store: open invoices: %w", err)
	}
	out := make([]OpenInvoiceRow, len(rows))
	for i, r := range rows {
		out[i] = OpenInvoiceRow{ID: r.ID, Number: r.Number, Period: r.Period, Currency: r.Currency, TotalMinor: r.TotalMinor,
			DueAt: r.DueAt, Attempts: int(r.Attempts), NextAttempt: at(r.NextAttemptAt)}
	}
	return out, nil
}

// PlanNames are a plan's names.
func PlanNames(ctx context.Context, tx Tx, plan ulid.ID) (ar, en string, err error) {
	r, err := billingdb.New(tx).GetPlanNames(ctx, plan)
	if err != nil {
		return "", "", fmt.Errorf("billing store: plan names: %w", err)
	}
	return r.NameAr, r.NameEn, nil
}
