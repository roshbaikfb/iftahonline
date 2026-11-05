-- name: GetBillingSettings :one
-- The console's collection settings (ADR-0083): one row.
SELECT prepaid_enabled, card_enabled, minimum_egp_minor, minimum_sar_minor, minimum_aed_minor
FROM billing_settings WHERE id = 1;

-- name: SaveBillingSettings :execrows
UPDATE billing_settings SET prepaid_enabled = @prepaid_enabled, card_enabled = @card_enabled,
  minimum_egp_minor = @minimum_egp_minor, minimum_sar_minor = @minimum_sar_minor, minimum_aed_minor = @minimum_aed_minor,
  updated_at = now()
WHERE id = 1;

-- name: GetAccountBalance :one
-- The balance of the account tx is behind.
SELECT kind, currency, balance_minor, collection FROM billing_accounts WHERE id = @id;

-- name: LockAccountBalance :one
-- The balance, locked until the entry that changes it is written.
SELECT kind, currency, balance_minor, collection FROM billing_accounts WHERE id = @id FOR UPDATE;

-- name: SetAccountBalance :exec
UPDATE billing_accounts SET balance_minor = @balance_minor, updated_at = now() WHERE id = @id;

-- name: InsertBalanceEntry :execrows
-- An order's fee or refund, or an invoice, is written once: a repeat
-- writes nothing (balance_entries_order, balance_entries_invoice).
INSERT INTO balance_entries (id, account_id, kind, amount_minor, balance_after_minor, currency, reason, reference, admin_id, order_id, invoice_id,
                             checkout_id)
VALUES (@id, @account_id, @kind, @amount_minor, @balance_after_minor, @currency, sqlc.narg(reason), @reference, sqlc.narg(admin_id),
        sqlc.narg(order_id), sqlc.narg(invoice_id), sqlc.narg(checkout_id))
ON CONFLICT DO NOTHING;

-- name: ListBalanceEntries :many
-- The account's latest entries, newest first.
SELECT id, kind, amount_minor, balance_after_minor, currency, reason, reference, admin_id, order_id, invoice_id, checkout_id, created_at
FROM balance_entries
WHERE account_id = @account_id
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: LockOpenInvoiceIDs :many
-- The account's open invoices, locked until they are voided (a switch to
-- pay as you sell, ADR-0087).
SELECT id FROM invoices WHERE account_id = @account_id AND status = 'open' ORDER BY issued_at FOR UPDATE;

-- name: VoidInvoice :execrows
UPDATE invoices SET status = 'void', next_attempt_at = NULL WHERE id = @id AND status = 'open';

-- name: UnbillFees :execrows
-- A voided invoice's fees are due again, and its credits waiting again.
UPDATE sell_fees SET status = CASE status WHEN 'invoiced' THEN 'due' ELSE 'credit_due' END, invoice_id = NULL, updated_at = now()
WHERE store_id = @store_id AND invoice_id = @invoice_id AND status IN ('invoiced', 'credited');

-- name: SetAccountCollection :exec
UPDATE billing_accounts SET collection = @collection, updated_at = now() WHERE id = @id;

-- name: HasPrepaidDue :one
-- Fees taken from the balance and not invoiced yet: the month's invoice
-- (paid from the balance) still has to take their tax.
SELECT EXISTS (SELECT 1 FROM sell_fees WHERE store_id = @store_id AND prepaid AND status IN ('due', 'credit_due')) AS pending;
