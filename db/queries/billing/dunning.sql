-- name: DunningDue :many
-- A store without an account (never invoiced) cannot be due; coalesce
-- keeps the column non-null for the scan.
SELECT d.task::text AS task, d.id::uuid AS id, coalesce(d.account_id, d.id)::uuid AS account_id FROM app.dunning_due(@now::timestamptz) AS d(task, id, account_id);

-- name: GetOpenInvoice :one
-- The invoice to charge, locked until the charge is recorded.
SELECT id, number, currency, total_minor, attempts, first_failed_at, purpose, plan_id, cycle, for_store_id
FROM invoices WHERE id = @id AND status = 'open'
FOR UPDATE;

-- name: GetPaymentMethod :one
SELECT gateway, token, label FROM billing_payment_methods WHERE account_id = @account_id;

-- name: SavePaymentMethod :exec
INSERT INTO billing_payment_methods (account_id, gateway, token, label) VALUES (@account_id, @gateway, @token, @label)
ON CONFLICT (account_id) DO UPDATE SET gateway = excluded.gateway, token = excluded.token, label = excluded.label, updated_at = now();

-- name: InsertPaymentAttempt :exec
INSERT INTO payment_attempts (id, invoice_id, account_id, gateway, outcome, reason, reference, amount_minor)
VALUES (@id, @invoice_id, @account_id, @gateway, @outcome, @reason, @reference, @amount_minor);

-- name: MarkInvoicePaid :execrows
UPDATE invoices SET status = 'paid', paid_at = @paid_at, next_attempt_at = NULL, last_error = NULL
WHERE id = @id AND status = 'open';

-- name: MarkInvoiceFailed :exec
UPDATE invoices SET attempts = attempts + 1, first_failed_at = coalesce(first_failed_at, @failed_at),
  next_attempt_at = @next_attempt_at, last_error = @last_error
WHERE id = @id AND status = 'open';

-- name: GetRenewalInvoice :one
SELECT renewal_invoice_id, renewal_plan_id FROM subscriptions WHERE store_id = @store_id;

-- name: ClearRenewal :exec
UPDATE subscriptions SET renewal_invoice_id = NULL, renewal_plan_id = NULL, updated_at = now() WHERE store_id = @store_id;
