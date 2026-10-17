-- name: RiverSchemaVersion :one
-- Highest applied River migration; the worker is not ready below the
-- version its River library expects.
SELECT coalesce(max(version), 0)::bigint AS version
FROM river_migration
WHERE line = 'main';
