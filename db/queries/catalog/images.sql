-- name: InsertImage :one
INSERT INTO product_images (id, store_id, product_id, upload_key, alt, position)
VALUES (@id, @store_id, @product_id, @upload_key, @alt, @position)
RETURNING *;

-- name: CountProductImages :one
SELECT count(*) FROM product_images WHERE store_id = @store_id AND product_id = @product_id;

-- name: GetImage :one
SELECT * FROM product_images WHERE store_id = @store_id AND product_id = @product_id AND id = @id;

-- name: GetImageByID :one
SELECT * FROM product_images WHERE store_id = @store_id AND id = @id;

-- name: StartImageProcessing :one
UPDATE product_images SET status = 'processing', updated_at = now()
WHERE store_id = @store_id AND product_id = @product_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: MarkImageReady :one
UPDATE product_images
SET status = 'ready', object_key = @object_key, format = @format, width = @width, height = @height,
    bytes = @bytes, failure_reason = '', updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'processing'
RETURNING *;

-- name: MarkImageFailed :one
UPDATE product_images SET status = 'failed', failure_reason = @failure_reason, updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'processing'
RETURNING *;

-- name: UpdateImage :one
UPDATE product_images SET alt = @alt, position = @position, updated_at = now()
WHERE store_id = @store_id AND product_id = @product_id AND id = @id
RETURNING *;

-- name: DeleteImage :one
DELETE FROM product_images WHERE store_id = @store_id AND product_id = @product_id AND id = @id
RETURNING upload_key, object_key;

-- name: DeletePendingImage :one
DELETE FROM product_images WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING upload_key;

-- name: DeleteProductImages :many
DELETE FROM product_images WHERE store_id = @store_id AND product_id = @product_id
RETURNING upload_key, object_key;

-- name: ListImagesByProducts :many
-- One index lookup per product, like ListVariantsByProducts; the LIMIT is
-- domain.MaxProductImages.
SELECT i.*
FROM unnest(@product_ids::uuid[]) WITH ORDINALITY AS ids(product_id, ord)
CROSS JOIN LATERAL (
  SELECT * FROM product_images im
  WHERE im.store_id = @store_id AND im.product_id = ids.product_id
  ORDER BY im.position, im.id
  LIMIT 20) i
ORDER BY ids.ord, i.position, i.id;
