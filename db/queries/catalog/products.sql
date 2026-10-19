-- name: InsertProduct :one
INSERT INTO products (id, store_id, slug, title, description, status, options)
VALUES (@id, @store_id, @slug, @title, @description, @status, @options)
RETURNING *;

-- name: GetProduct :one
SELECT * FROM products
WHERE store_id = @store_id AND id = @id AND deleted_at IS NULL;

-- name: LockProduct :one
SELECT * FROM products
WHERE store_id = @store_id AND id = @id AND deleted_at IS NULL
FOR UPDATE;

-- name: UpdateProduct :one
UPDATE products
SET slug = @slug, title = @title, description = @description, status = @status,
    options = @options, updated_at = now()
WHERE store_id = @store_id AND id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProduct :execrows
UPDATE products SET deleted_at = now(), updated_at = now()
WHERE store_id = @store_id AND id = @id AND deleted_at IS NULL;

-- name: ListProducts :many
-- Newest first; `after` is the last ID of the previous page.
SELECT * FROM products p
WHERE p.store_id = @store_id AND p.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(after)::uuid IS NULL OR p.id < sqlc.narg(after)::uuid)
  AND (sqlc.narg(category_id)::uuid IS NULL OR EXISTS (
    SELECT 1 FROM product_categories pc
    WHERE pc.store_id = p.store_id AND pc.category_id = sqlc.narg(category_id)::uuid AND pc.product_id = p.id))
ORDER BY p.id DESC
LIMIT @lim;

-- name: ListProductCategories :many
SELECT product_id, category_id FROM product_categories
WHERE store_id = @store_id AND product_id = ANY(@product_ids::uuid[])
ORDER BY product_id, category_id;

-- name: DeleteProductCategories :exec
DELETE FROM product_categories WHERE store_id = @store_id AND product_id = @product_id;

-- name: InsertProductCategories :exec
INSERT INTO product_categories (store_id, product_id, category_id)
SELECT @store_id, @product_id, c FROM unnest(@category_ids::uuid[]) AS c;
