-- Platform support on a store's team (C1): the team as the console sees
-- it, delayed requests, their cancel links, and account actions.

-- name: ListTeam :many
-- The store's members for the console, in one query: two-step state,
-- account status and when each last used the dashboard (support sessions
-- left out; zero time: never).
SELECT m.user_id, m.role, m.created_at, u.name, u.email::text AS email, coalesce(u.phone, '')::text AS phone,
       (u.totp_enabled_at IS NOT NULL)::bool AS mfa_enabled, u.status,
       coalesce((SELECT max(s.last_seen_at) FROM user_sessions s WHERE s.user_id = u.id AND s.impersonator_id IS NULL),
                '0001-01-01T00:00:00Z')::timestamptz AS last_seen_at
FROM store_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.store_id = @store_id
ORDER BY m.created_at, m.user_id;

-- name: MemberStores :many
-- The stores a user works on, with the role (the console's pool):
-- through app.member_stores, IDs only.
SELECT store_id::uuid, role::text FROM app.member_stores(@user_id::uuid);

-- name: SetUserStatus :execrows
UPDATE users SET status = @status, updated_at = now() WHERE id = @id AND status <> @status;

-- name: InsertSupportRequest :one
INSERT INTO support_requests (id, store_id, kind, user_id, from_user_id, requested_by, reason, effective_at)
VALUES (@id, @store_id, @kind, @user_id, sqlc.narg(from_user_id), @requested_by, @reason, @effective_at)
RETURNING *;

-- name: GetSupportRequest :one
SELECT * FROM support_requests WHERE store_id = @store_id AND id = @id;

-- name: LockSupportRequest :one
SELECT * FROM support_requests WHERE store_id = @store_id AND id = @id FOR UPDATE;

-- name: ListSupportRequests :many
-- The store's open requests and those closed since @since, newest first.
SELECT * FROM support_requests
WHERE store_id = @store_id AND (cancelled_at IS NULL AND done_at IS NULL OR coalesce(cancelled_at, done_at) >= @since)
ORDER BY created_at DESC, id DESC
LIMIT 20;

-- name: CancelSupportRequest :execrows
UPDATE support_requests SET cancelled_at = now(), cancelled_by = @cancelled_by
WHERE store_id = @store_id AND id = @id AND cancelled_at IS NULL AND done_at IS NULL;

-- name: FinishSupportRequest :execrows
UPDATE support_requests SET done_at = now()
WHERE store_id = @store_id AND id = @id AND cancelled_at IS NULL AND done_at IS NULL;

-- name: InsertSupportToken :exec
-- A request's cancel link for one recipient (several requests may each
-- send one to the same user: no older link is closed).
INSERT INTO user_tokens (id, user_id, purpose, token_hash, email, expires_at, request_id, link_store_id)
VALUES (@id, @user_id, 'support_cancel', @token_hash, @email, @expires_at, @request_id, @link_store_id);

-- name: PeekSupportToken :one
-- The request an open cancel link names, without using it.
SELECT request_id::uuid AS request_id, link_store_id::uuid AS store_id, user_id, email::text AS email FROM user_tokens
WHERE token_hash = @token_hash AND purpose = 'support_cancel' AND used_at IS NULL AND expires_at > now();

-- name: CloseSupportTokens :exec
-- A request closed (cancelled or done) closes all its links.
UPDATE user_tokens SET used_at = now()
WHERE request_id = @request_id AND purpose = 'support_cancel' AND used_at IS NULL;

-- name: SupportTokenForSend :one
-- The cancel link an email job sends: still open.
SELECT t.id, t.user_id, t.email::text AS email, t.request_id::uuid AS request_id, u.name
FROM user_tokens t JOIN users u ON u.id = t.user_id
WHERE t.id = @id AND t.purpose = 'support_cancel' AND t.used_at IS NULL AND t.expires_at > now();
