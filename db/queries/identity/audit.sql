-- name: InsertAuditEntry :exec
INSERT INTO audit_log (id, store_id, actor_user_id, action, target_type, target_id, ip_prefix, metadata)
VALUES (@id, @store_id, sqlc.narg(actor_user_id), @action, @target_type, @target_id, @ip_prefix, @metadata);

-- name: ListAuditEntries :many
-- Newest first, keyset-paginated on (occurred_at, id).
SELECT * FROM audit_log
WHERE store_id = @store_id
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (occurred_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT @lim;

-- name: EnsureAuditPartitions :one
SELECT app.ensure_audit_log_partitions(@months_ahead::int)::int AS created;
