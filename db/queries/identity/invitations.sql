-- name: InsertInvitation :one
INSERT INTO store_invitations (id, store_id, email, role, token_hash, invited_by, expires_at)
VALUES (@id, @store_id, @email, @role, @token_hash, @invited_by, @expires_at)
RETURNING *;

-- name: ListOpenInvitations :many
SELECT * FROM store_invitations
WHERE store_id = @store_id AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 200;

-- name: GetInvitation :one
SELECT * FROM store_invitations WHERE store_id = @store_id AND id = @id;

-- name: GetOpenInvitationByToken :one
SELECT * FROM store_invitations
WHERE store_id = @store_id AND token_hash = @token_hash
  AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: RevokeInvitation :execrows
UPDATE store_invitations SET revoked_at = now()
WHERE store_id = @store_id AND id = @id AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: AcceptInvitation :execrows
UPDATE store_invitations SET accepted_at = now()
WHERE store_id = @store_id AND id = @id AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: CountSeats :one
-- What the plan's team limit counts: members, the owner included, and
-- open invitations (each may become a member).
SELECT (SELECT count(*) FROM store_memberships m WHERE m.store_id = @store_id)
     + (SELECT count(*) FROM store_invitations i
        WHERE i.store_id = @store_id AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()) AS n;
