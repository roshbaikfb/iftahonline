-- name: InsertSession :one
INSERT INTO user_sessions (id, user_id, token_hash, user_agent, ip_prefix, expires_at)
VALUES (@id, @user_id, @token_hash, @user_agent, @ip_prefix, @expires_at)
RETURNING *;

-- name: GetActiveSession :one
-- One query per authenticated request: the live session and its user.
SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, u.status AS user_status
FROM user_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = @token_hash AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: GetActiveSessionInStore :one
-- As GetActiveSession, plus the user's role in the tenant store (empty for
-- a non-member). Runs in a tenant transaction, so RLS limits the
-- membership join to that store.
SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, u.status AS user_status,
       coalesce(m.role, '')::text AS role
FROM user_sessions s
JOIN users u ON u.id = s.user_id
LEFT JOIN store_memberships m ON m.user_id = s.user_id AND m.store_id = @store_id
WHERE s.token_hash = @token_hash AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE user_sessions SET last_seen_at = now(), expires_at = @expires_at
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE user_sessions SET revoked_at = now()
WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL;

-- name: ListUserSessions :many
SELECT * FROM user_sessions
WHERE user_id = @user_id AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 100;
