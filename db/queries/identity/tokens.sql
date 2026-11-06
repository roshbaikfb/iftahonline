-- One-time email links (D3): user_tokens, platform-wide like users.

-- name: InsertUserToken :exec
INSERT INTO user_tokens (id, user_id, purpose, token_hash, email, expires_at)
VALUES (@id, @user_id, @purpose, @token_hash, @email, @expires_at);

-- name: UseUserToken :one
-- Takes an open link once: unused, unexpired, of its purpose. No row:
-- unknown, used or expired.
UPDATE user_tokens SET used_at = now()
WHERE token_hash = @token_hash AND purpose = @purpose AND used_at IS NULL AND expires_at > now()
RETURNING id, user_id, email;

-- name: GetOpenUserTokenEmail :one
-- The email an open link is for, without using it.
SELECT email FROM user_tokens
WHERE token_hash = @token_hash AND purpose = @purpose AND used_at IS NULL AND expires_at > now();

-- name: GetUserTokenForSend :one
-- The link an email job sends: still open.
SELECT t.id, t.user_id, t.purpose, t.email, t.expires_at, u.name
FROM user_tokens t JOIN users u ON u.id = t.user_id
WHERE t.id = @id AND t.used_at IS NULL AND t.expires_at > now();

-- name: CloseUserTokens :exec
-- A new link of a purpose closes the older ones (only the latest works).
UPDATE user_tokens SET used_at = now()
WHERE user_id = @user_id AND purpose = @purpose AND used_at IS NULL;

-- name: SetEmailVerified :execrows
-- Confirms the address the link was sent to, if it is still the user's.
UPDATE users SET email_verified_at = now(), updated_at = now()
WHERE id = @id AND email = @email AND email_verified_at IS NULL;

-- name: RevokeUserSessions :exec
-- Signs a user out everywhere but one session (none: everywhere).
UPDATE user_sessions SET revoked_at = now()
WHERE user_id = @user_id AND revoked_at IS NULL AND (sqlc.narg(keep)::uuid IS NULL OR id <> sqlc.narg(keep)::uuid);
