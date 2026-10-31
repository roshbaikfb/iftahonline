-- Every query on a tenant table takes store_id; the store layer reads it
-- from tenant.FromContext (CLAUDE.md rule 5). RLS checks it again.
--
-- Stores' columns are listed, never *: the legacy plan column is not
-- read (billing owns plans, ADR-0045) and is dropped once no release
-- reads it (expand, migrate, contract).

-- name: InsertStore :one
INSERT INTO stores (id, slug, name, currency, locale, timezone)
VALUES (@id, @slug, @name, @currency, @locale, @timezone)
RETURNING id, slug, name, status, currency, locale, timezone, created_at, updated_at, billing_hold;

-- name: GetStore :one
SELECT id, slug, name, status, currency, locale, timezone, created_at, updated_at, billing_hold FROM stores WHERE id = @store_id;

-- name: UpdateStore :one
UPDATE stores
SET name = @name, locale = @locale, timezone = @timezone, updated_at = now()
WHERE id = @store_id
RETURNING id, slug, name, status, currency, locale, timezone, created_at, updated_at, billing_hold;

-- name: SetStoreStatus :one
UPDATE stores SET status = @status, updated_at = now()
WHERE id = @store_id
RETURNING id, slug, name, status, currency, locale, timezone, created_at, updated_at, billing_hold;

-- name: ResolveHost :one
-- The only cross-tenant read: SECURITY DEFINER, returns no other columns.
SELECT r.store_id::uuid AS store_id, r.store_status::text AS store_status
FROM app.resolve_host(@host::text) AS r;

-- name: SetBillingHold :execrows
-- Holds or releases the store for billing (ADR-0051); no row when it
-- already was.
UPDATE stores SET billing_hold = @hold, updated_at = now()
WHERE id = @store_id AND billing_hold IS DISTINCT FROM @hold;
