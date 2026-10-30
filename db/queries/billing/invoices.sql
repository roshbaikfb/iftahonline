-- name: UseAccount :exec
-- Puts the transaction behind the account's wall (ADR-0049).
SELECT set_config('app.account_id', sqlc.arg(account_id)::uuid::text, true);

-- name: UseStore :exec
-- Moves the transaction to another store's wall, for an agency's stores.
SELECT set_config('app.store_id', sqlc.arg(store_id)::uuid::text, true);

-- name: BillableStores :many
SELECT s::uuid AS store_id FROM app.billable_stores(@period::date) AS s ORDER BY 1;

-- name: GetSubscriptionAccount :one
SELECT account_id FROM subscriptions WHERE store_id = @store_id;

-- name: SetSubscriptionAccount :execrows
UPDATE subscriptions SET account_id = @account_id, updated_at = now() WHERE store_id = @store_id AND account_id IS NULL;

-- name: InsertAccount :exec
INSERT INTO billing_accounts (id, kind, name, currency, country) VALUES (@id, @kind, @name, @currency, @country);

-- name: InsertAccountStore :exec
INSERT INTO billing_account_stores (account_id, member_store_id) VALUES (@account_id, @member_store_id);

-- name: GetAccount :one
SELECT id, kind, name, currency, country FROM billing_accounts WHERE id = @id;

-- name: ListAccountStores :many
SELECT member_store_id FROM billing_account_stores WHERE account_id = @account_id ORDER BY added_at, member_store_id LIMIT 1000;

-- name: InvoiceExists :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE account_id = @account_id AND period = @period);

-- name: GetTaxRate :one
SELECT rate_bps FROM tax_rates WHERE country = @country;

-- name: NextInvoiceNumber :one
-- Gapless: the counter's row is locked until the invoice commits.
INSERT INTO invoice_counters (year, last) VALUES (@year, 1)
ON CONFLICT (year) DO UPDATE SET last = invoice_counters.last + 1
RETURNING last;

-- name: InsertInvoice :exec
-- Its first charge is due at once (ADR-0051).
INSERT INTO invoices (id, account_id, number, period, currency, subtotal_minor, tax_bps, tax_minor, total_minor, issued_at, due_at, next_attempt_at)
VALUES (@id, @account_id, @number, @period, @currency, @subtotal_minor, @tax_bps, @tax_minor, @total_minor, @issued_at, @due_at, @issued_at);

-- name: InsertInvoiceLine :exec
INSERT INTO invoice_lines (invoice_id, account_id, line_no, kind, line_store_id, description, quantity, amount_minor)
VALUES (@invoice_id, @account_id, @line_no, @kind, @line_store_id, @description, @quantity, @amount_minor);

-- name: RenewalDue :one
-- The store's paid subscription whose period ends by renews_by and is
-- not billed yet, with the price of its plan version and cycle.
SELECT p.kind, p.name_ar, s.cycle, pp.amount_minor
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
JOIN plan_prices pp ON pp.plan_id = s.plan_id AND pp.currency = @currency AND pp.cycle = s.cycle
WHERE s.store_id = @store_id AND s.status = 'active' AND NOT s.cancel_at_period_end AND s.renewal_invoice_id IS NULL
  AND p.kind IN ('subscription', 'agency') AND s.period_end <= @renews_by;

-- name: MarkRenewalBilled :exec
UPDATE subscriptions SET renewal_invoice_id = @invoice_id, updated_at = now() WHERE store_id = @store_id;

-- name: SumDueFees :one
-- Due up to the period: a fee recorded after its month's invoice (a late
-- event) goes on the next one.
SELECT count(*)::bigint AS n, coalesce(sum(fee_minor), 0)::bigint AS total
FROM sell_fees WHERE store_id = @store_id AND period <= @period AND status = 'due' AND currency = @currency;

-- name: SumCreditFees :one
SELECT count(*)::bigint AS n, coalesce(sum(fee_minor), 0)::bigint AS total
FROM sell_fees WHERE store_id = @store_id AND status = 'credit_due' AND currency = @currency;

-- name: MarkFeesInvoiced :execrows
UPDATE sell_fees SET status = 'invoiced', invoice_id = @invoice_id, updated_at = now()
WHERE store_id = @store_id AND period <= @period AND status = 'due' AND currency = @currency;

-- name: MarkCreditsUsed :execrows
UPDATE sell_fees SET status = 'credited', invoice_id = @invoice_id, updated_at = now()
WHERE store_id = @store_id AND status = 'credit_due' AND currency = @currency;
