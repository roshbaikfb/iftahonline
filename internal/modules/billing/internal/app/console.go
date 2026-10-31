package app

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// What the console reads and does about billing (ADR-0058).

// Health is the platform's subscriptions and invoices, counted.
type Health = store.Health

// OpenInvoice is an open invoice of an account.
type OpenInvoice = store.OpenInvoiceRow

var errInvoiceNotOpen = errs.NotFound("invoice_not_found", "no open invoice with this ID for the store's account")

// Health counts every store's subscription by status and the open and
// overdue invoices.
func (s *Service) Health(ctx context.Context) (h Health, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		h, txErr = store.PlatformHealth(ctx, tx, s.now())
		return txErr
	})
	return h, err
}

// SettleStoreInvoice records invoice, an open invoice of the store-in-
// ctx's account, as paid outside the card on file (a transfer the
// platform confirmed, under reference): the account's stores settle as
// on any payment (ADR-0051). Any other invoice is 404.
func (s *Service) SettleStoreInvoice(ctx context.Context, invoice ulid.ID, reference string) error {
	var account ulid.ID
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		id, ok, err := accountOf(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return errInvoiceNotOpen
		}
		if err = store.UseAccount(ctx, tx, id); err != nil {
			return err
		}
		_, open, err := store.LockOpenInvoice(ctx, tx, invoice)
		if err == nil && !open {
			err = errInvoiceNotOpen
		}
		if err != nil {
			return err
		}
		account = id
		return nil
	})
	if err != nil {
		return err
	}
	return s.SettleInvoice(ctx, account, invoice, reference)
}

// DuplicatePayment is a payment of an invoice no longer open, to refund
// by hand (ADR-0063).
type DuplicatePayment = store.DuplicatePayment

var (
	errNoDuplicate     = errs.NotFound("duplicate_not_found", "no duplicate payment to refund with this ID")
	errRefundReference = errs.Validation(errs.FieldError{Field: "reference", Code: "invalid", Message: "1 to 120 characters"})
)

// DuplicatePayments lists every account's duplicate payments, those to
// refund first.
func (s *Service) DuplicatePayments(ctx context.Context) (out []DuplicatePayment, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.DuplicatePayments(ctx, tx)
		return txErr
	})
	return out, err
}

// MarkRefunded records duplicate payment id refunded on the gateway under
// reference; one already refunded, or no such duplicate, is 404.
func (s *Service) MarkRefunded(ctx context.Context, id ulid.ID, reference string) error {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 120 {
		return errRefundReference
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		account, ok, err := store.DuplicateAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return errNoDuplicate
		}
		if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		done, err := store.MarkRefunded(ctx, tx, id, reference, s.now().UTC())
		if err == nil && !done {
			err = errNoDuplicate
		}
		return err
	})
}
