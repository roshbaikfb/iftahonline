-- name: GetTheme :one
SELECT settings, version, updated_at FROM store_themes WHERE store_id = @store_id;

-- name: InsertTheme :one
-- The first save. No row back means another save created it first.
INSERT INTO store_themes (store_id, settings) VALUES (@store_id, @settings)
ON CONFLICT (store_id) DO NOTHING
RETURNING settings, version, updated_at;

-- name: UpdateTheme :one
-- Optimistic concurrency: no row back means the version moved on.
UPDATE store_themes SET settings = @settings, version = version + 1, updated_at = now()
WHERE store_id = @store_id AND version = @version
RETURNING settings, version, updated_at;
