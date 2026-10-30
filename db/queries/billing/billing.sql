-- name: GetSubscription :one
-- The store's subscription and its plan's kind; no row is the free plan.
SELECT s.plan_id, p.kind, s.status, s.cycle, s.trial_ends_at, s.period_start, s.period_end,
       s.grace_until, s.suspended_at, s.cancel_at_period_end, s.trial_used
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
