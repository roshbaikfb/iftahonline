-- name: ConsoleInvoices :many
-- Nullable columns come with a flag (the function's types are unknown
-- to sqlc).
SELECT id::uuid, number::text, account_id::uuid, account_kind::text, account_name::text, (store_id IS NOT NULL)::bool AS has_store,
       coalesce(store_id, account_id)::uuid AS store_id, period::date, currency::text, subtotal_minor::bigint, tax_bps::int,
       tax_minor::bigint, total_minor::bigint, status::text, issued_at::timestamptz, due_at::timestamptz,
       (paid_at IS NOT NULL)::bool AS paid, coalesce(paid_at, issued_at)::timestamptz AS paid_at, attempts::int, last_error::text,
       (next_attempt_at IS NOT NULL)::bool AS has_next_attempt, coalesce(next_attempt_at, issued_at)::timestamptz AS next_attempt_at
FROM app.billing_invoices(sqlc.narg(id)::uuid, @status::text, @currency::text, @q::text, @now::timestamptz,
                          sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid, @lim::int);

-- name: ConsoleAttempts :many
SELECT id::uuid, created_at::timestamptz, invoice_id::uuid, invoice_number::text, account_id::uuid, account_kind::text,
       account_name::text, (store_id IS NOT NULL)::bool AS has_store, coalesce(store_id, account_id)::uuid AS store_id, gateway::text,
       outcome::text, reason::text, reference::text, amount_minor::bigint, currency::text, (refunded_at IS NOT NULL)::bool AS refunded,
       coalesce(refunded_at, created_at)::timestamptz AS refunded_at
FROM app.billing_attempts(sqlc.narg(invoice_id)::uuid, @outcome::text, @gateway::text, sqlc.narg(before_at)::timestamptz,
                          sqlc.narg(before_id)::uuid, @lim::int);

-- name: ConsoleSummary :many
SELECT kind::text, currency::text, outcome::text, reason::text, n::bigint, total_minor::bigint, tax_minor::bigint
FROM app.billing_summary(@now::timestamptz, @month_start::timestamptz, @week_ago::timestamptz);

-- name: ListInvoiceLines :many
-- An invoice's lines, behind its account's wall.
SELECT kind, description, quantity, amount_minor, line_store_id FROM invoice_lines WHERE invoice_id = @invoice_id ORDER BY line_no;

-- name: ConsoleAttemptsByReference :many
SELECT id::uuid, created_at::timestamptz, invoice_id::uuid, invoice_number::text, account_id::uuid, account_kind::text,
       account_name::text, (store_id IS NOT NULL)::bool AS has_store, coalesce(store_id, account_id)::uuid AS store_id, gateway::text,
       outcome::text, reason::text, reference::text, amount_minor::bigint, currency::text, (refunded_at IS NOT NULL)::bool AS refunded,
       coalesce(refunded_at, created_at)::timestamptz AS refunded_at
FROM app.billing_attempts_by_reference(@reference::text, @lim::int);
