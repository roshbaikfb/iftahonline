-- Super-admin console queries. They run only on the app_admin pool; the
-- merchant role (app_user) has no privileges on these tables at all.

-- name: InsertAdmin :one
INSERT INTO platform_admins (id, email, name, password_hash, totp_secret)
VALUES (@id, @email, @name, @password_hash, @totp_secret)
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
SELECT s.id, s.admin_id, s.created_at, s.last_seen_at, s.expires_at, a.status AS admin_status
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
SELECT * FROM admin_audit_log
WHERE sqlc.narg(before_at)::timestamptz IS NULL
   OR (occurred_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid)
ORDER BY occurred_at DESC, id DESC
LIMIT @lim;

-- name: JobHealth :one
-- River's queue for the health page (ADR-0058): counts only.
SELECT available::bigint, running::bigint, retryable::bigint, discarded_24h::bigint, oldest_available_seconds::bigint
FROM app.job_health(@now::timestamptz);
