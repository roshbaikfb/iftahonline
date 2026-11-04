package store

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's invoices and payments page: every account's, through
// the app.billing_* functions (migration 00090).

// ConsoleInvoice is an invoice as the console lists it.
type ConsoleInvoice struct {
	ID, Account              ulid.ID
	Number                   string
	AccountKind, AccountName string
	Store                    *ulid.ID // the invoice's store, else its account's first
	Period                   time.Time
	Currency                 string
	SubtotalMinor            int64
	TaxBps                   int
	TaxMinor, TotalMinor     int64
	Status                   string
	IssuedAt, DueAt          time.Time
	PaidAt, NextAttemptAt    *time.Time
	Attempts                 int
	LastError                string
}

// InvoiceFilter narrows ConsoleInvoices: one invoice (ID), or a page
// after (BeforeAt, BeforeID). Empty strings match anything; Status
// overdue is an open invoice due before Now.
type InvoiceFilter struct {
	ID               *ulid.ID
	Status, Currency string
	Q                string // a number prefix or a part of the name, LIKE-escaped
	Now              time.Time
	BeforeAt         *time.Time
	BeforeID         *ulid.ID
	Limit            int
}

// ConsoleInvoices lists every account's invoices through f, newest
// first.
func ConsoleInvoices(ctx context.Context, tx Tx, f InvoiceFilter) ([]ConsoleInvoice, error) {
	rows, err := billingdb.New(tx).ConsoleInvoices(ctx, billingdb.ConsoleInvoicesParams{ID: f.ID, Status: f.Status, Currency: f.Currency,
		Q: f.Q, Now: f.Now, BeforeAt: f.BeforeAt, BeforeID: f.BeforeID, Lim: int32(min(f.Limit, 1000))}) //nolint:gosec // bounded
	if err != nil {
		return nil, fmt.Errorf("billing store: console invoices: %w", err)
	}
	out := make([]ConsoleInvoice, len(rows))
	for i, r := range rows {
		inv := ConsoleInvoice{ID: r.ID, Account: r.AccountID, Number: r.Number, AccountKind: r.AccountKind, AccountName: r.AccountName,
			Period: r.Period, Currency: r.Currency, SubtotalMinor: r.SubtotalMinor, TaxBps: int(r.TaxBps), TaxMinor: r.TaxMinor,
			TotalMinor: r.TotalMinor, Status: r.Status, IssuedAt: r.IssuedAt, DueAt: r.DueAt, Attempts: int(r.Attempts), LastError: r.LastError}
		if r.HasStore {
			inv.Store = &rows[i].StoreID
		}
		if r.Paid {
			inv.PaidAt = &rows[i].PaidAt
		}
		if r.HasNextAttempt {
			inv.NextAttemptAt = &rows[i].NextAttemptAt
		}
		out[i] = inv
	}
	return out, nil
}

// ConsolePayment is a payment attempt as the console lists it.
type ConsolePayment struct {
	ID, Invoice, Account     ulid.ID
	CreatedAt                time.Time
	InvoiceNumber            string
	AccountKind, AccountName string
	Store                    *ulid.ID
	Gateway, Outcome         string
	Reason, Reference        string
	AmountMinor              int64
	Currency                 string
	RefundedAt               *time.Time
}

// PaymentFilter narrows ConsolePayments: one invoice's, or every one;
// empty strings match anything.
type PaymentFilter struct {
	Invoice          *ulid.ID
	Outcome, Gateway string
	BeforeAt         *time.Time
	BeforeID         *ulid.ID
	Limit            int
}

// ConsolePayments lists every account's payment attempts through f,
// newest first.
func ConsolePayments(ctx context.Context, tx Tx, f PaymentFilter) ([]ConsolePayment, error) {
	rows, err := billingdb.New(tx).ConsoleAttempts(ctx, billingdb.ConsoleAttemptsParams{InvoiceID: f.Invoice, Outcome: f.Outcome,
		Gateway: f.Gateway, BeforeAt: f.BeforeAt, BeforeID: f.BeforeID, Lim: int32(min(f.Limit, 1000))}) //nolint:gosec // bounded
	if err != nil {
		return nil, fmt.Errorf("billing store: console payments: %w", err)
	}
	return toPayments(rows), nil
}

// PaymentsByReference lists the payment attempts whose gateway reference
// is exactly reference (at most limit), newest first.
func PaymentsByReference(ctx context.Context, tx Tx, reference string, limit int) ([]ConsolePayment, error) {
	rows, err := billingdb.New(tx).ConsoleAttemptsByReference(ctx, billingdb.ConsoleAttemptsByReferenceParams{Reference: reference,
		Lim: int32(min(limit, 100))}) //nolint:gosec // bounded
	if err != nil {
		return nil, fmt.Errorf("billing store: payments by reference: %w", err)
	}
	same := make([]billingdb.ConsoleAttemptsRow, len(rows))
	for i, r := range rows {
		same[i] = billingdb.ConsoleAttemptsRow(r)
	}
	return toPayments(same), nil
}

func toPayments(rows []billingdb.ConsoleAttemptsRow) []ConsolePayment {
	out := make([]ConsolePayment, len(rows))
	for i, r := range rows {
		p := ConsolePayment{ID: r.ID, Invoice: r.InvoiceID, Account: r.AccountID, CreatedAt: r.CreatedAt, InvoiceNumber: r.InvoiceNumber,
			AccountKind: r.AccountKind, AccountName: r.AccountName, Gateway: r.Gateway, Outcome: r.Outcome, Reason: r.Reason,
			Reference: r.Reference, AmountMinor: r.AmountMinor, Currency: r.Currency}
		if r.HasStore {
			p.Store = &rows[i].StoreID
		}
		if r.Refunded {
			p.RefundedAt = &rows[i].RefundedAt
		}
		out[i] = p
	}
	return out
}

// SummaryRow is one of app.billing_summary's counts: Kind open, overdue
// or collected per Currency, or failed per Outcome and Reason.
type SummaryRow struct {
	Kind, Currency, Outcome, Reason string
	Count, TotalMinor, TaxMinor     int64
}

// ConsoleSummary counts open and overdue invoices at now, those paid
// since monthStart, and payments failed since weekAgo.
func ConsoleSummary(ctx context.Context, tx Tx, now, monthStart, weekAgo time.Time) ([]SummaryRow, error) {
	rows, err := billingdb.New(tx).ConsoleSummary(ctx, billingdb.ConsoleSummaryParams{Now: now, MonthStart: monthStart, WeekAgo: weekAgo})
	if err != nil {
		return nil, fmt.Errorf("billing store: console summary: %w", err)
	}
	out := make([]SummaryRow, len(rows))
	for i, r := range rows {
		out[i] = SummaryRow{Kind: r.Kind, Currency: r.Currency, Outcome: r.Outcome, Reason: r.Reason, Count: r.N, TotalMinor: r.TotalMinor,
			TaxMinor: r.TaxMinor}
	}
	return out, nil
}

// InvoiceLine is a line of an invoice.
type InvoiceLine struct {
	Kind        string
	Description string
	Quantity    int64
	AmountMinor int64
	Store       *ulid.ID
}

// InvoiceLines lists invoice's lines; tx must be behind its account's
// wall (UseAccount).
func InvoiceLines(ctx context.Context, tx Tx, invoice ulid.ID) ([]InvoiceLine, error) {
	rows, err := billingdb.New(tx).ListInvoiceLines(ctx, invoice)
	if err != nil {
		return nil, fmt.Errorf("billing store: invoice lines: %w", err)
	}
	out := make([]InvoiceLine, len(rows))
	for i, r := range rows {
		out[i] = InvoiceLine{Kind: r.Kind, Description: r.Description, Quantity: r.Quantity, AmountMinor: r.AmountMinor, Store: r.LineStoreID}
	}
	return out, nil
}

// MonthCollected is the revenue collected in one month and currency.
type MonthCollected struct {
	Month         time.Time // the month's first day, midnight UTC
	Currency      string
	Invoices      int64
	SubtotalMinor int64
}

// Collected is the revenue collected per month in tz since since: paid
// invoices, before VAT (ADR-0081).
func Collected(ctx context.Context, tx Tx, since time.Time, tz string) ([]MonthCollected, error) {
	rows, err := billingdb.New(tx).ConsoleCollected(ctx, billingdb.ConsoleCollectedParams{Since: since, Tz: tz})
	if err != nil {
		return nil, fmt.Errorf("billing store: collected: %w", err)
	}
	out := make([]MonthCollected, len(rows))
	for i, r := range rows {
		out[i] = MonthCollected{Month: r.Month, Currency: r.Currency, Invoices: r.Invoices, SubtotalMinor: r.SubtotalMinor}
	}
	return out, nil
}

// Converted are the stores that paid a subscription since since.
func Converted(ctx context.Context, tx Tx, since time.Time) ([]ulid.ID, error) {
	ids, err := billingdb.New(tx).ConsoleConverted(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("billing store: converted: %w", err)
	}
	return ids, nil
}
