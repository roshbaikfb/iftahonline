-- name: InsertDomain :one
INSERT INTO store_domains (id, store_id, host, kind, status, is_primary, verification_token, verified_at)
VALUES (@id, @store_id, @host, @kind, @status, @is_primary, @verification_token,
        CASE WHEN @status::text = 'verified' THEN now() END)
RETURNING *;

-- name: ListDomains :many
SELECT * FROM store_domains WHERE store_id = @store_id ORDER BY created_at, id;

-- name: GetDomain :one
SELECT * FROM store_domains WHERE store_id = @store_id AND id = @id;

-- name: CountCustomDomains :one
SELECT count(*) FROM store_domains WHERE store_id = @store_id AND kind = 'custom';

-- name: ListStoreHosts :many
SELECT host::text FROM store_domains WHERE store_id = @store_id ORDER BY host;

-- name: DeleteCustomDomain :execrows
DELETE FROM store_domains
WHERE store_id = @store_id AND id = @id AND kind = 'custom' AND NOT is_primary;

-- name: ClearPrimaryDomain :exec
UPDATE store_domains SET is_primary = false WHERE store_id = @store_id AND is_primary;

-- name: SetPrimaryDomain :one
UPDATE store_domains SET is_primary = true
WHERE store_id = @store_id AND id = @id AND status = 'verified'
RETURNING *;

-- name: MarkDomainVerified :one
UPDATE store_domains
SET status = 'verified', verified_at = now(), last_checked_at = now(), failure_reason = ''
WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: MarkDomainCheckFailed :one
UPDATE store_domains
SET last_checked_at = now(), check_attempts = check_attempts + 1, failure_reason = @failure_reason
WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: MarkDomainFailed :one
UPDATE store_domains
SET status = 'failed', last_checked_at = now(), failure_reason = @failure_reason
WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: ResetDomainPending :one
UPDATE store_domains
SET status = 'pending', check_attempts = 0, failure_reason = ''
WHERE store_id = @store_id AND id = @id AND status IN ('pending', 'failed')
RETURNING *;
