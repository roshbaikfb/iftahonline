-- name: SetUserScope :exec
-- SET LOCAL app.user_id for the member_self_read policy (ADR-0007 §4).
SELECT set_config('app.user_id', (sqlc.arg(user_id)::uuid)::text, true);

-- name: InsertMembership :one
INSERT INTO store_memberships (store_id, user_id, role)
VALUES (@store_id, @user_id, @role)
RETURNING *;

-- name: ListMyMemberships :many
-- In a user-scoped transaction: the member_self_read policy returns only
-- the user's own rows, across stores.
SELECT store_id, role, created_at FROM store_memberships
WHERE user_id = @user_id
ORDER BY created_at, store_id;
