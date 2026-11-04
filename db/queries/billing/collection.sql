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

-- name: InsertBalanceEntry :exec
INSERT INTO balance_entries (id, account_id, kind, amount_minor, balance_after_minor, currency, reason, reference, admin_id)
VALUES (@id, @account_id, @kind, @amount_minor, @balance_after_minor, @currency, sqlc.narg(reason), @reference, sqlc.narg(admin_id));

-- name: ListBalanceEntries :many
-- The account's latest entries, newest first.
SELECT id, kind, amount_minor, balance_after_minor, currency, reason, reference, admin_id, created_at
FROM balance_entries
WHERE account_id = @account_id
ORDER BY created_at DESC, id DESC
LIMIT @lim;
