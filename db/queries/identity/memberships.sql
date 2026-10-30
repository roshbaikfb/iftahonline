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

-- name: ListMembers :many
-- One query for the whole team (no N+1): memberships joined to users.
SELECT m.user_id, m.role, m.created_at, u.name, u.email
FROM store_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.store_id = @store_id
ORDER BY m.created_at, m.user_id;

-- name: IsMemberByEmail :one
SELECT EXISTS (
  SELECT 1 FROM store_memberships m JOIN users u ON u.id = m.user_id
  WHERE m.store_id = @store_id AND u.email = @email
) AS member;

-- name: GetMemberRole :one
SELECT role FROM store_memberships WHERE store_id = @store_id AND user_id = @user_id;

-- name: UpdateMemberRole :execrows
-- The owner's role is never changed here (ownership transfer is separate).
UPDATE store_memberships SET role = @role, updated_at = now()
WHERE store_id = @store_id AND user_id = @user_id AND role <> 'owner';

-- name: DeleteMember :execrows
DELETE FROM store_memberships
WHERE store_id = @store_id AND user_id = @user_id AND role <> 'owner';

-- name: ListMemberPhones :many
-- The verified phones of the store's members with one of roles, for the
-- platform's WhatsApp notices to them.
SELECT u.phone::text AS phone FROM store_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.store_id = @store_id AND m.role = ANY(@roles::text[]) AND u.phone IS NOT NULL AND u.phone_verified_at IS NOT NULL
ORDER BY m.created_at
LIMIT 20;
