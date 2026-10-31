-- name: ConsumeUsage :one
-- Adds n to the month's count unless that passes max_used; no row means
-- it would (the caller checked n <= max_used for the first insert).
INSERT INTO usage_counters (store_id, feature, period, used)
VALUES (@store_id, @feature, @period, @n::bigint)
ON CONFLICT (store_id, feature, period) DO UPDATE
SET used = usage_counters.used + excluded.used, updated_at = now()
WHERE usage_counters.used + excluded.used <= @max_used::bigint
RETURNING used;

-- name: ReleaseUsage :one
-- Gives n back to the month it was taken from, never below zero.
UPDATE usage_counters
SET used = greatest(used - @n::bigint, 0), updated_at = now()
WHERE store_id = @store_id AND feature = @feature AND period = @period
RETURNING used;

-- name: GetUsage :one
SELECT used FROM usage_counters
WHERE store_id = @store_id AND feature = @feature AND period = @period;

-- name: LockFeature :exec
-- Serializes a store's creations of one counted feature until the
-- transaction ends (AdmitTx).
SELECT pg_advisory_xact_lock(hashtextextended(concat('entitlements:', sqlc.arg(store_id)::uuid, ':', sqlc.arg(feature)::text), 0));

-- name: ListOverrides :many
-- The store's per-store grants (ADR-0057), expired ones included.
SELECT feature, value, expires_at, reason, updated_at FROM entitlement_overrides
WHERE store_id = @store_id ORDER BY feature;

-- name: UpsertOverride :exec
INSERT INTO entitlement_overrides (store_id, feature, value, expires_at, reason)
VALUES (@store_id, @feature, @value, @expires_at, @reason)
ON CONFLICT (store_id, feature) DO UPDATE
SET value = excluded.value, expires_at = excluded.expires_at, reason = excluded.reason, updated_at = now();

-- name: DeleteOverride :execrows
DELETE FROM entitlement_overrides WHERE store_id = @store_id AND feature = @feature;
