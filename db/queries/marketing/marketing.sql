-- name: ListPixels :many
-- A store has at most one row per provider (five).
SELECT * FROM store_pixels WHERE store_id = @store_id ORDER BY provider;

-- name: GetPixel :one
SELECT * FROM store_pixels WHERE store_id = @store_id AND provider = @provider;

-- name: InsertPixel :one
-- A first save; ON CONFLICT means another save came first (the caller
-- read version 0 and lost the race).
INSERT INTO store_pixels (store_id, provider, id, pixel_id, label, test_code, enabled, server_token)
VALUES (@store_id, @provider, @id, @pixel_id, @label, @test_code, @enabled, sqlc.narg(server_token))
ON CONFLICT (store_id, provider) DO NOTHING
RETURNING *;

-- name: UpdatePixel :one
-- Over the version read; no row means it changed since.
UPDATE store_pixels
SET pixel_id = @pixel_id, label = @label, test_code = @test_code, enabled = @enabled,
    server_token = sqlc.narg(server_token), version = version + 1, updated_at = now()
WHERE store_id = @store_id AND provider = @provider AND version = @version
RETURNING *;

-- name: DeletePixel :execrows
DELETE FROM store_pixels WHERE store_id = @store_id AND provider = @provider;

-- name: GetSettings :one
SELECT * FROM marketing_settings WHERE store_id = @store_id;

-- name: InsertSettings :one
INSERT INTO marketing_settings (store_id, consent)
VALUES (@store_id, @consent)
ON CONFLICT (store_id) DO NOTHING
RETURNING *;

-- name: UpdateSettings :one
UPDATE marketing_settings SET consent = @consent, version = version + 1, updated_at = now()
WHERE store_id = @store_id AND version = @version
RETURNING *;

-- name: GetFeed :one
SELECT * FROM product_feeds WHERE store_id = @store_id;

-- name: SetFeedKey :one
-- A new key replaces the old one: the old URLs stop working.
INSERT INTO product_feeds (store_id, key)
VALUES (@store_id, @key)
ON CONFLICT (store_id) DO UPDATE SET key = EXCLUDED.key, created_at = now()
RETURNING *;

-- name: DeleteFeed :execrows
DELETE FROM product_feeds WHERE store_id = @store_id;
