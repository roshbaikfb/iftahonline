-- Paying pay-as-you-sell fees from the prepaid balance (ADR-0084,
-- approved 2026-11-05).
--
-- A delivered order's fee is taken from the balance of an account that
-- chose it (billing_accounts.collection = 'balance'), and its return puts
-- it back at once: one entry per order and kind (a repeated event writes
-- nothing). sell_fees.prepaid marks the fees so taken.
--
-- The month's invoice stays the tax document. It is paid from the
-- balance, which then gives only what the entries have not: its tax
-- (approved: one entry, so the balance always equals the invoices). That
-- is an 'invoice' entry, signed.
--
-- Expand only: a defaulted column, nullable columns, a new index, and the
-- new table's checks replaced (NOT VALID; validated in a later migration).

-- +goose Up
ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS prepaid boolean NOT NULL DEFAULT false;

ALTER TABLE balance_entries ADD COLUMN IF NOT EXISTS order_id uuid;
ALTER TABLE balance_entries ADD COLUMN IF NOT EXISTS invoice_id uuid;
-- One fee and one refund per order, one entry per invoice.
CREATE UNIQUE INDEX IF NOT EXISTS balance_entries_order ON balance_entries (account_id, kind, order_id) WHERE order_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS balance_entries_invoice ON balance_entries (invoice_id) WHERE invoice_id IS NOT NULL;

ALTER TABLE balance_entries DROP CONSTRAINT IF EXISTS balance_entries_kind_check;
ALTER TABLE balance_entries ADD CONSTRAINT balance_entries_kind_check
  CHECK (kind IN ('topup', 'manual', 'fee', 'fee_refund', 'invoice')) NOT VALID;
ALTER TABLE balance_entries ADD CONSTRAINT balance_entries_order_check
  CHECK ((kind IN ('fee', 'fee_refund')) = (order_id IS NOT NULL) AND (kind = 'invoice') = (invoice_id IS NOT NULL)) NOT VALID;

-- +goose Down
ALTER TABLE balance_entries DROP CONSTRAINT IF EXISTS balance_entries_order_check;
ALTER TABLE balance_entries DROP CONSTRAINT IF EXISTS balance_entries_kind_check;
ALTER TABLE balance_entries ADD CONSTRAINT balance_entries_kind_check
  CHECK (kind IN ('topup', 'manual', 'fee', 'fee_refund')) NOT VALID;
DROP INDEX IF EXISTS balance_entries_invoice;
DROP INDEX IF EXISTS balance_entries_order;
ALTER TABLE balance_entries DROP COLUMN IF EXISTS invoice_id;
ALTER TABLE balance_entries DROP COLUMN IF EXISTS order_id;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS prepaid;
