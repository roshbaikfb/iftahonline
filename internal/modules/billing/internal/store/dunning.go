package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Task is work the hourly dunning run found (app.dunning_due).
type Task struct {
	Kind    string  // charge, suspend or cancel
	ID      ulid.ID // the invoice to charge, or the store
	Account ulid.ID
}

// DunningDue lists the work due by now.
func DunningDue(ctx context.Context, tx Tx, now time.Time) ([]Task, error) {
	rows, err := billingdb.New(tx).DunningDue(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("billing store: dunning due: %w", err)
	}
	out := make([]Task, len(rows))
	for i, r := range rows {
		out[i] = Task{Kind: r.Task, ID: r.ID, Account: r.AccountID}
	}
	return out, nil
}

// OpenInvoice is an invoice waiting for its payment.
type OpenInvoice struct {
	ID          ulid.ID
	Number      string
	Currency    string
	TotalMinor  int64
	Attempts    int
	FirstFailed time.Time
	// A first period paid on the gateway's page (ADR-0060): for ForStore,
	// of Plan and Cycle. Zero for a monthly invoice.
	Subscribe *Subscribe
}

// Subscribe is what a first-period invoice starts once paid.
type Subscribe struct {
	ForStore, Plan ulid.ID
	Cycle          domain.Cycle
}

// LockOpenInvoice reads an open invoice of the account tx is behind and
// locks it until tx ends; ok is false when it is not open (paid).
func LockOpenInvoice(ctx context.Context, tx Tx, id ulid.ID) (OpenInvoice, bool, error) {
	r, err := billingdb.New(tx).GetOpenInvoice(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return OpenInvoice{}, false, nil
	case err != nil:
		return OpenInvoice{}, false, fmt.Errorf("billing store: open invoice: %w", err)
	}
	inv := OpenInvoice{ID: r.ID, Number: r.Number, Currency: r.Currency, TotalMinor: r.TotalMinor, Attempts: int(r.Attempts),
		FirstFailed: at(r.FirstFailedAt)}
	if r.Purpose == "subscribe" && r.PlanID != nil && r.ForStoreID != nil && r.Cycle != nil {
		c, err := domain.ParseCycle(*r.Cycle)
		if err != nil {
			return OpenInvoice{}, false, err
		}
		inv.Subscribe = &Subscribe{ForStore: *r.ForStoreID, Plan: *r.PlanID, Cycle: c}
	}
	return inv, true, nil
}

// Method is an account's card on file; Token is sealed.
type Method struct {
	Gateway string
	Token   []byte
	Label   string
}

// PaymentMethod is the card on file of the account tx is behind; ok is
// false when it has none.
func PaymentMethod(ctx context.Context, tx Tx, account ulid.ID) (Method, bool, error) {
	r, err := billingdb.New(tx).GetPaymentMethod(ctx, account)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Method{}, false, nil
	case err != nil:
		return Method{}, false, fmt.Errorf("billing store: payment method: %w", err)
	}
	return Method{Gateway: r.Gateway, Token: r.Token, Label: r.Label}, true, nil
}

// SavePaymentMethod saves or replaces the account's card on file.
func SavePaymentMethod(ctx context.Context, tx Tx, account ulid.ID, m Method) error {
	if err := billingdb.New(tx).SavePaymentMethod(ctx, billingdb.SavePaymentMethodParams{
		AccountID: account, Gateway: m.Gateway, Token: m.Token, Label: m.Label,
	}); err != nil {
		return fmt.Errorf("billing store: save payment method: %w", err)
	}
	return nil
}

// Attempt is one try at paying an invoice.
type Attempt struct {
	ID          ulid.ID
	Invoice     ulid.ID
	Account     ulid.ID
	Gateway     string
	Outcome     string // approved, declined, no_method, manual
	Reason      string
	Reference   string
	AmountMinor int64
}

// InsertAttempt records an attempt.
func InsertAttempt(ctx context.Context, tx Tx, a Attempt) error {
	if err := billingdb.New(tx).InsertPaymentAttempt(ctx, billingdb.InsertPaymentAttemptParams{
		ID: a.ID, InvoiceID: a.Invoice, AccountID: a.Account, Gateway: a.Gateway, Outcome: a.Outcome,
		Reason: a.Reason, Reference: a.Reference, AmountMinor: a.AmountMinor,
	}); err != nil {
		return fmt.Errorf("billing store: insert attempt: %w", err)
	}
	return nil
}

// MarkInvoicePaid closes an open invoice; ok is false when it was not
// open.
func MarkInvoicePaid(ctx context.Context, tx Tx, id ulid.ID, paidAt time.Time) (bool, error) {
	n, err := billingdb.New(tx).MarkInvoicePaid(ctx, billingdb.MarkInvoicePaidParams{ID: id, PaidAt: &paidAt})
	if err != nil {
		return false, fmt.Errorf("billing store: invoice paid: %w", err)
	}
	return n == 1, nil
}

// MarkInvoiceFailed counts a failed attempt; next is the retry (zero:
// none is left).
func MarkInvoiceFailed(ctx context.Context, tx Tx, id ulid.ID, failedAt, next time.Time, reason string) error {
	if err := billingdb.New(tx).MarkInvoiceFailed(ctx, billingdb.MarkInvoiceFailedParams{
		ID: id, FailedAt: &failedAt, NextAttemptAt: ptr(next), LastError: &reason,
	}); err != nil {
		return fmt.Errorf("billing store: invoice failed: %w", err)
	}
	return nil
}

// BilledRenewal is the invoice that billed the store-in-ctx's renewal
// and the plan version it charged for (ADR-0056).
type BilledRenewal struct {
	Invoice ulid.ID
	Plan    *ulid.ID // nil for renewals billed before versions renewed
}

// RenewalInvoice is the store-in-ctx's billed renewal, if any.
func RenewalInvoice(ctx context.Context, tx Tx) (*BilledRenewal, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	r, err := billingdb.New(tx).GetRenewalInvoice(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && r.RenewalInvoiceID == nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("billing store: renewal invoice: %w", err)
	}
	return &BilledRenewal{Invoice: *r.RenewalInvoiceID, Plan: r.RenewalPlanID}, nil
}

// ClearRenewal forgets the billed renewal of the store in ctx: its
// period is paid.
func ClearRenewal(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = billingdb.New(tx).ClearRenewal(ctx, store); err != nil {
		return fmt.Errorf("billing store: clear renewal: %w", err)
	}
	return nil
}

// DuplicatePayment is a payment of an invoice no longer open, for the
// console to refund by hand (ADR-0063).
type DuplicatePayment struct {
	ID, Account, Store ulid.ID
	InvoiceNumber      string
	Currency           string
	AmountMinor        int64
	Gateway, Reference string // the gateway's transaction, to refund
	CreatedAt          time.Time
	RefundedAt         *time.Time
	RefundReference    string
}

// InsertDuplicate records a duplicate payment once per gateway
// transaction; false when it was already recorded.
func InsertDuplicate(ctx context.Context, tx Tx, a Attempt) (bool, error) {
	n, err := billingdb.New(tx).InsertDuplicateAttempt(ctx, billingdb.InsertDuplicateAttemptParams{
		ID: a.ID, InvoiceID: a.Invoice, AccountID: a.Account, Gateway: a.Gateway, Reference: a.Reference, AmountMinor: a.AmountMinor,
	})
	if err != nil {
		return false, fmt.Errorf("billing store: insert duplicate: %w", err)
	}
	return n == 1, nil
}

// DuplicatePayments lists every account's duplicates, those to refund
// first (at most 200), through app.duplicate_payments.
func DuplicatePayments(ctx context.Context, tx Tx) ([]DuplicatePayment, error) {
	rows, err := billingdb.New(tx).ListDuplicatePayments(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: duplicate payments: %w", err)
	}
	out := make([]DuplicatePayment, len(rows))
	for i, r := range rows {
		out[i] = DuplicatePayment{ID: r.ID, Account: r.AccountID, Store: r.StoreID, InvoiceNumber: r.InvoiceNumber, Currency: r.Currency,
			AmountMinor: r.AmountMinor, Gateway: r.Gateway, Reference: r.Reference, CreatedAt: r.CreatedAt, RefundReference: r.RefundReference}
		if r.Refunded {
			at := r.RefundedAt
			out[i].RefundedAt = &at
		}
	}
	return out, nil
}

// DuplicateAccount is the account of duplicate payment id; ok is false
// for no such duplicate.
func DuplicateAccount(ctx context.Context, tx Tx, id ulid.ID) (ulid.ID, bool, error) {
	account, err := billingdb.New(tx).DuplicatePaymentAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ulid.ID{}, false, nil
	}
	if err != nil {
		return ulid.ID{}, false, fmt.Errorf("billing store: duplicate account: %w", err)
	}
	return account, true, nil
}

// MarkRefunded records duplicate id refunded under reference; false when
// it was already, or is no duplicate of the account in tx.
func MarkRefunded(ctx context.Context, tx Tx, id ulid.ID, reference string, at time.Time) (bool, error) {
	n, err := billingdb.New(tx).MarkAttemptRefunded(ctx, billingdb.MarkAttemptRefundedParams{ID: id, RefundedAt: &at, RefundReference: &reference})
	if err != nil {
		return false, fmt.Errorf("billing store: mark refunded: %w", err)
	}
	return n == 1, nil
}
