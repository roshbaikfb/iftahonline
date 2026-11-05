-- Topping up the prepaid balance on Paymob's page (ADR-0085).
--
-- A top-up is a billing checkout with no invoice: purpose 'topup'. Its
-- confirmed payment is a 'topup' entry, written in the transaction that
-- marks the checkout paid, so a repeated callback adds nothing; the
-- entry keeps the checkout's reference (balance_entries_checkout).
--
-- Expand only: invoice_id becomes nullable, a defaulted column, a new
-- index, new checks NOT VALID. 00099's checks on balance_entries are
-- validated here.

-- +goose Up
ALTER TABLE billing_checkouts ALTER COLUMN invoice_id DROP NOT NULL;
ALTER TABLE billing_checkouts ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'invoice';
ALTER TABLE billing_checkouts ADD CONSTRAINT billing_checkouts_purpose_check
  CHECK (purpose IN ('invoice', 'topup') AND (purpose = 'invoice') = (invoice_id IS NOT NULL)) NOT VALID;

ALTER TABLE balance_entries ADD COLUMN IF NOT EXISTS checkout_id uuid;
CREATE UNIQUE INDEX IF NOT EXISTS balance_entries_checkout ON balance_entries (checkout_id) WHERE checkout_id IS NOT NULL;

ALTER TABLE balance_entries VALIDATE CONSTRAINT balance_entries_kind_check;
ALTER TABLE balance_entries VALIDATE CONSTRAINT balance_entries_order_check;

-- +goose Down
DROP INDEX IF EXISTS balance_entries_checkout;
ALTER TABLE balance_entries DROP COLUMN IF EXISTS checkout_id;
ALTER TABLE billing_checkouts DROP CONSTRAINT IF EXISTS billing_checkouts_purpose_check;
ALTER TABLE billing_checkouts DROP COLUMN IF EXISTS purpose;
