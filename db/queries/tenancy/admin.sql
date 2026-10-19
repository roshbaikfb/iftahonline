-- Cross-tenant store directory for the super-admin console. These run
-- ONLY on the app_admin pool, whose admin_read policies allow SELECT on
-- every store; as app_user they would return nothing (RLS).

-- name: AdminSearchStores :many
SELECT s.*,
       coalesce((SELECT d.host::text FROM store_domains d WHERE d.store_id = s.id AND d.is_primary), '')::text AS primary_host
FROM stores s
WHERE (@query::text = '' OR s.slug::text LIKE @query::text || '%' OR s.name ILIKE '%' || @query::text || '%')
  AND (@status::text = '' OR s.status = @status::text)
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (s.created_at, s.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY s.created_at DESC, s.id DESC
LIMIT @lim;

-- name: AdminGetStore :one
SELECT * FROM stores WHERE id = @id;

-- name: AdminListDomains :many
SELECT * FROM store_domains WHERE store_id = @store_id ORDER BY created_at, id;
