-- The console's plan catalog (ADR-0056), run as app_admin.

-- name: ListFeatures :many
SELECT key, kind, name_ar, name_en, sort FROM features ORDER BY sort, key;

-- name: GetFeatureKind :one
SELECT kind FROM features WHERE key = @key;

-- name: UpsertFeature :exec
-- A key's kind never changes: the caller checked it first.
INSERT INTO features (key, kind, name_ar, name_en, sort) VALUES (@key, @kind, @name_ar, @name_en, @sort)
ON CONFLICT (key) DO UPDATE SET name_ar = excluded.name_ar, name_en = excluded.name_en, sort = excluded.sort, updated_at = now();

-- name: ListPlans :many
SELECT id, code, version, kind, name_ar, name_en, trial_days, public, status = 'active' AS active, sort
FROM plans ORDER BY sort, code, version DESC;

-- name: ListAllPlanPrices :many
SELECT plan_id, currency, cycle, amount_minor FROM plan_prices ORDER BY plan_id, currency, cycle;

-- name: ListAllPlanGrants :many
SELECT plan_id, feature, value FROM plan_entitlements ORDER BY plan_id, feature;

-- name: ListAllPlanFees :many
SELECT plan_id, currency, rate_bps, min_minor, max_minor FROM plan_fees ORDER BY plan_id, currency;

-- name: LockPlanCode :exec
-- Serializes publications of one plan code until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended(concat('billing:plan:', sqlc.arg(code)::text), 0));

-- name: LatestPlanVersion :one
-- The code's newest version and kind, active or not.
SELECT version, kind FROM plans WHERE code = @code ORDER BY version DESC LIMIT 1;

-- name: RetireActivePlan :execrows
UPDATE plans SET status = 'retired', updated_at = now() WHERE code = @code AND status = 'active';

-- name: GetActivePlanKind :one
SELECT kind FROM plans WHERE code = @code AND status = 'active';

-- name: InsertPlan :exec
INSERT INTO plans (id, code, version, kind, name_ar, name_en, trial_days, public, sort)
VALUES (@id, @code, @version, @kind, @name_ar, @name_en, @trial_days, @public, @sort);

-- name: InsertPlanPrice :exec
INSERT INTO plan_prices (plan_id, currency, cycle, amount_minor) VALUES (@plan_id, @currency, @cycle, @amount_minor);

-- name: InsertPlanGrant :exec
INSERT INTO plan_entitlements (plan_id, feature, value) VALUES (@plan_id, @feature, @value);

-- name: InsertPlanFee :exec
INSERT INTO plan_fees (plan_id, currency, rate_bps, min_minor, max_minor) VALUES (@plan_id, @currency, @rate_bps, @min_minor, @max_minor);
