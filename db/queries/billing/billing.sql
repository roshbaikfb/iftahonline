-- name: GetSubscription :one
-- The store's subscription and its plan's kind; no row is the free plan.
SELECT s.plan_id, p.kind, s.status, s.cycle, s.trial_ends_at, s.period_start, s.period_end,
       s.grace_until, s.suspended_at, s.cancel_at_period_end, s.trial_used, s.next_plan_id, s.next_cycle
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
WHERE s.store_id = @store_id;

-- name: GetFreePlan :one
-- The plan a store falls back to (plans_active_free_key keeps it one).
SELECT id, code, version, kind, name_ar, name_en, trial_days, public, status = 'active' AS active, sort
FROM plans
WHERE kind = 'free' AND status = 'active';

-- name: ListPlanGrants :many
SELECT feature, value FROM plan_entitlements WHERE plan_id = @plan_id ORDER BY feature;

-- name: PeriodsEnded :many
SELECT s::uuid AS store_id FROM app.periods_ended(@now::timestamptz) AS s;

-- name: SaveSubscription :exec
UPDATE subscriptions SET plan_id = @plan_id, status = @status, cycle = @cycle, trial_ends_at = @trial_ends_at,
  period_start = @period_start, period_end = @period_end, grace_until = @grace_until, suspended_at = @suspended_at,
  cancel_at_period_end = @cancel_at_period_end, trial_used = @trial_used, next_plan_id = @next_plan_id,
  next_cycle = @next_cycle, updated_at = now()
WHERE store_id = @store_id;

-- name: BillingHealth :one
-- Counts for the console's health page (ADR-0058).
SELECT trialing::bigint, active::bigint, past_due::bigint, suspended::bigint, cancelled::bigint,
       invoices_open::bigint, invoices_overdue::bigint
FROM app.billing_health(@now::timestamptz);

-- name: GetPlan :one
SELECT id, code, version, kind, name_ar, name_en, trial_days, public, status = 'active' AS active, sort
FROM plans WHERE id = @id;

-- name: GetPlanPrice :one
SELECT amount_minor FROM plan_prices WHERE plan_id = @plan_id AND currency = @currency AND cycle = @cycle;
