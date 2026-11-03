-- Super-admin console queries. They run only on the app_admin pool; the
-- merchant role (app_user) has no privileges on these tables at all.

-- name: InsertAdmin :one
INSERT INTO platform_admins (id, email, name, password_hash, totp_secret, role)
VALUES (@id, @email, @name, @password_hash, @totp_secret, @role)
RETURNING *;

-- name: GetAdminByEmail :one
SELECT * FROM platform_admins WHERE email = @email;

-- name: GetAdmin :one
SELECT * FROM platform_admins WHERE id = @id;

-- name: UseAdminTOTPStep :execrows
-- Atomic replay guard, as for merchants.
UPDATE platform_admins SET totp_last_step = @step WHERE id = @id AND totp_last_step < @step;

-- name: RecordAdminLogin :exec
UPDATE platform_admins SET last_login_at = now() WHERE id = @id;

-- name: InsertAdminSession :one
INSERT INTO admin_sessions (id, admin_id, token_hash, user_agent, ip_prefix, expires_at)
VALUES (@id, @admin_id, @token_hash, @user_agent, @ip_prefix, @expires_at)
RETURNING *;

-- name: GetActiveAdminSession :one
SELECT s.id, s.admin_id, s.created_at, s.last_seen_at, s.expires_at, a.status AS admin_status, a.role AS admin_role
FROM admin_sessions s
JOIN platform_admins a ON a.id = s.admin_id
WHERE s.token_hash = @token_hash AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: TouchAdminSession :exec
UPDATE admin_sessions SET last_seen_at = now(), expires_at = @expires_at
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeAdminSession :exec
UPDATE admin_sessions SET revoked_at = now() WHERE id = @id AND revoked_at IS NULL;

-- name: InsertAdminAudit :exec
INSERT INTO admin_audit_log (id, admin_id, action, target_type, target_id, ip_prefix, metadata)
VALUES (@id, sqlc.narg(admin_id), @action, @target_type, @target_id, @ip_prefix, @metadata);

-- name: ListAdminAudit :many
-- A page of the log with each admin's name. Every filter is optional: an
-- admin, action prefixes (a family), a store (its own actions and its
-- invoices', whose metadata names it), and a time range.
SELECT l.id, l.occurred_at, l.admin_id, l.action, l.target_type, l.target_id, l.ip_prefix, l.metadata,
       coalesce(a.name, '')::text AS admin_name
FROM admin_audit_log l
LEFT JOIN platform_admins a ON a.id = l.admin_id
WHERE (sqlc.narg(before_at)::timestamptz IS NULL
       OR (l.occurred_at, l.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
  AND (sqlc.narg(admin_id)::uuid IS NULL OR l.admin_id = sqlc.narg(admin_id)::uuid)
  AND (cardinality(@prefixes::text[]) = 0 OR l.action LIKE ANY (@prefixes::text[]))
  AND (@store::text = '' OR (l.target_type = 'store' AND l.target_id = @store::text) OR l.metadata ->> 'store' = @store::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR l.occurred_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR l.occurred_at < sqlc.narg(until)::timestamptz)
ORDER BY l.occurred_at DESC, l.id DESC
LIMIT @lim;

-- name: ListAdmins :many
-- The console's admins for the log's filter: no secrets.
SELECT id, name, email::text AS email, status = 'active' AS active FROM platform_admins ORDER BY name, id;

-- name: JobHealth :one
-- River's queue for the health page (ADR-0058): counts only.
SELECT available::bigint, running::bigint, retryable::bigint, discarded_24h::bigint, oldest_available_seconds::bigint
FROM app.job_health(@now::timestamptz);
