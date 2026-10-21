-- name: EnsureMonthlyPartitions :one
-- Keeps every table registered in app.monthly_partitioned partitioned
-- months_ahead months ahead (migration 00022).
SELECT app.ensure_monthly_partitions(@months_ahead::int)::int AS created;
