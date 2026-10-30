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
