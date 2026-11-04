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

-- name: ListTeam :many
-- The team page (ADR-0078): every admin with its role, state and last
-- sign-in, and whether a reset is waiting. No secrets.
SELECT a.id, a.name, a.email::text AS email, a.role, a.status, a.created_at, a.last_login_at,
       EXISTS (SELECT 1 FROM admin_invites i WHERE i.admin_id = a.id AND i.purpose = 'reset'
               AND i.used_at IS NULL AND i.cancelled_at IS NULL)::bool AS resetting
FROM platform_admins a
ORDER BY (a.status = 'active') DESC, a.name, a.id;

-- name: ListOpenInvites :many
-- New admins invited and not joined yet, expired ones included.
SELECT i.id, i.admin_id, i.email::text AS email, i.name, i.role, i.created_at, i.expires_at, coalesce(b.name, '')::text AS invited_by_name
FROM admin_invites i
LEFT JOIN platform_admins b ON b.id = i.invited_by
WHERE i.purpose = 'join' AND i.used_at IS NULL AND i.cancelled_at IS NULL
ORDER BY i.created_at DESC
LIMIT 200;

-- name: InsertInvite :exec
INSERT INTO admin_invites (id, token_hash, purpose, admin_id, email, name, role, totp_secret, invited_by, expires_at)
VALUES (@id, @token_hash, @purpose, @admin_id, @email, @name, @role, @totp_secret, @invited_by, @expires_at);

-- name: CancelOpenInvites :execrows
-- Closes an admin's open invite (one at most), before a new one.
UPDATE admin_invites SET cancelled_at = now() WHERE admin_id = @admin_id AND used_at IS NULL AND cancelled_at IS NULL;

-- name: GetOpenInvite :one
SELECT * FROM admin_invites WHERE id = @id AND used_at IS NULL AND cancelled_at IS NULL;

-- name: GetInviteByToken :one
-- An invite by its link, open or not: the caller tells why it fails.
SELECT i.*, coalesce(b.name, '')::text AS invited_by_name
FROM admin_invites i
LEFT JOIN platform_admins b ON b.id = i.invited_by
WHERE i.token_hash = @token_hash;

-- name: UseInvite :execrows
-- Once: a second accept finds nothing.
UPDATE admin_invites SET used_at = now() WHERE id = @id AND used_at IS NULL AND cancelled_at IS NULL AND expires_at > now();

-- name: InsertInvitedAdmin :exec
INSERT INTO platform_admins (id, email, name, password_hash, totp_secret, role)
VALUES (@id, @email, @name, @password_hash, @totp_secret, @role);

-- name: ResetAdminCredentials :execrows
-- A reset accepted: new password and seed, signed in from scratch.
UPDATE platform_admins SET password_hash = @password_hash, totp_secret = @totp_secret, totp_last_step = 0, status = 'active', updated_at = now()
WHERE id = @id;

-- name: LockOwners :many
-- The active owners, locked: the last one may not be demoted or disabled.
SELECT id FROM platform_admins WHERE role = 'owner' AND status = 'active' ORDER BY id FOR UPDATE;

-- name: SetAdminRole :execrows
UPDATE platform_admins SET role = @role, updated_at = now() WHERE id = @id;

-- name: SetAdminStatus :execrows
UPDATE platform_admins SET status = @status, updated_at = now() WHERE id = @id;

-- name: RevokeAdminSessions :execrows
-- Every live session of an admin, when it is disabled or reset.
UPDATE admin_sessions SET revoked_at = now() WHERE admin_id = @admin_id AND revoked_at IS NULL;
