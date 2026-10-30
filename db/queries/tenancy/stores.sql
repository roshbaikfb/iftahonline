-- Every query on a tenant table takes store_id; the store layer reads it
-- from tenant.FromContext (CLAUDE.md rule 5). RLS checks it again.

-- name: InsertStore :one
INSERT INTO stores (id, slug, name, currency, locale, timezone)
VALUES (@id, @slug, @name, @currency, @locale, @timezone)
RETURNING *;

-- name: GetStore :one
SELECT * FROM stores WHERE id = @store_id;

-- name: UpdateStore :one
UPDATE stores
SET name = @name, locale = @locale, timezone = @timezone, updated_at = now()
WHERE id = @store_id
RETURNING *;

-- name: SetStoreStatus :one
UPDATE stores SET status = @status, updated_at = now()
WHERE id = @store_id
RETURNING *;

-- name: ResolveHost :one
-- The only cross-tenant read: SECURITY DEFINER, returns no other columns.
SELECT r.store_id::uuid AS store_id, r.store_status::text AS store_status
FROM app.resolve_host(@host::text) AS r;

-- name: SetBillingHold :execrows
-- Holds or releases the store for billing (ADR-0051); no row when it
-- already was.
UPDATE stores SET billing_hold = @hold, updated_at = now()
WHERE id = @store_id AND billing_hold IS DISTINCT FROM @hold;
