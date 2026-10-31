-- Agencies set up from the console (ADR-0059).

-- name: InsertAgency :exec
INSERT INTO billing_accounts (id, kind, name, currency, country, plan_code)
VALUES (@id, 'agency', @name, @currency, @country, @plan_code);

-- name: ListAgencies :many
SELECT id::uuid AS id, name::text AS name, currency::text AS currency, plan_code::text AS plan_code,
       created_at::timestamptz AS created_at, stores::bigint AS stores
FROM app.agency_accounts();

-- name: LockAgency :one
-- The agency behind the transaction's account wall, locked so members
-- join and leave one at a time.
SELECT id, name, currency, country, plan_code::text AS plan_code, created_at FROM billing_accounts
WHERE id = @id AND kind = 'agency' FOR UPDATE;

-- name: GetActivePlanByCode :one
SELECT id, code, version, kind, name_ar, name_en, trial_days, public, status = 'active' AS active, sort
FROM plans WHERE code = @code AND status = 'active';

-- name: HasOpenInvoice :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE account_id = @account_id AND status = 'open');

-- name: DeleteAccountStore :execrows
DELETE FROM billing_account_stores WHERE account_id = @account_id AND member_store_id = @member_store_id;

-- name: PutSubscription :exec
-- The store's subscription as a whole, created when it had none, in
-- account (NULL: none yet); a billed renewal is forgotten.
INSERT INTO subscriptions (store_id, plan_id, status, cycle, trial_ends_at, period_start, period_end, grace_until,
                           suspended_at, cancel_at_period_end, trial_used, account_id, next_plan_id, next_cycle)
VALUES (@store_id, @plan_id, @status, @cycle, @trial_ends_at, @period_start, @period_end, @grace_until,
        @suspended_at, @cancel_at_period_end, @trial_used, @account_id, @next_plan_id, @next_cycle)
ON CONFLICT (store_id) DO UPDATE SET
  plan_id = excluded.plan_id, status = excluded.status, cycle = excluded.cycle, trial_ends_at = excluded.trial_ends_at,
  period_start = excluded.period_start, period_end = excluded.period_end, grace_until = excluded.grace_until,
  suspended_at = excluded.suspended_at, cancel_at_period_end = excluded.cancel_at_period_end,
  trial_used = excluded.trial_used, account_id = excluded.account_id, next_plan_id = excluded.next_plan_id,
  next_cycle = excluded.next_cycle, renewal_invoice_id = NULL, renewal_plan_id = NULL, updated_at = now();
