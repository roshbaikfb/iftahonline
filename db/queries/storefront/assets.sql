-- name: InsertAsset :one
INSERT INTO store_assets (id, store_id, kind, upload_key) VALUES (@id, @store_id, @kind, @upload_key)
RETURNING *;

-- name: GetAsset :one
SELECT * FROM store_assets WHERE store_id = @store_id AND id = @id;

-- name: GetReadyAsset :one
SELECT * FROM store_assets WHERE store_id = @store_id AND kind = @kind AND status = 'ready';

-- name: GetLatestUnreadyAsset :one
-- The newest upload that is not live yet: what the editor shows progress for.
SELECT * FROM store_assets
WHERE store_id = @store_id AND kind = @kind AND status <> 'ready'
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteUnreadyAssets :many
-- Starting a new upload abandons earlier unfinished ones of the kind.
DELETE FROM store_assets WHERE store_id = @store_id AND kind = @kind AND status <> 'ready'
RETURNING upload_key;

-- name: StartAssetProcessing :one
UPDATE store_assets SET status = 'processing', updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: DeleteReadyAsset :one
-- Removes the live asset of a kind (replaced or deleted by the merchant).
DELETE FROM store_assets WHERE store_id = @store_id AND kind = @kind AND status = 'ready'
RETURNING object_key;

-- name: MarkAssetReady :one
UPDATE store_assets
SET status = 'ready', object_key = @object_key, format = @format, width = @width, height = @height,
    bytes = @bytes, failure_reason = '', updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'processing'
RETURNING *;

-- name: MarkAssetFailed :one
UPDATE store_assets SET status = 'failed', failure_reason = @failure_reason, updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'processing'
RETURNING *;

-- name: DeletePendingAsset :one
DELETE FROM store_assets WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING upload_key;
