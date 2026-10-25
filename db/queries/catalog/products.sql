-- name: InsertProduct :one
INSERT INTO products (id, store_id, slug, title, description, status, options)
VALUES (@id, @store_id, @slug, @title, @description, @status, @options)
RETURNING *;

-- Product reads carry their category IDs inline: one PK lookup per
-- product (a separate "links for these products" query degrades into a
-- store-wide scan under generic plans for large stores; ADR-0011).

-- name: GetProduct :one
SELECT p.*, ARRAY(
    SELECT pc.category_id FROM product_categories pc
    WHERE pc.store_id = p.store_id AND pc.product_id = p.id ORDER BY pc.category_id)::uuid[] AS category_ids
FROM products p
WHERE p.store_id = @store_id AND p.id = @id AND p.deleted_at IS NULL;

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
SELECT p.*, ARRAY(
    SELECT pc.category_id FROM product_categories pc
    WHERE pc.store_id = p.store_id AND pc.product_id = p.id ORDER BY pc.category_id)::uuid[] AS category_ids
FROM products p
WHERE p.store_id = @store_id AND p.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(after)::uuid IS NULL OR p.id < sqlc.narg(after)::uuid)
ORDER BY p.id DESC
LIMIT @lim;

-- name: ListProductsInCategory :many
-- ListProducts for one category, driven by the category's links so a
-- small category in a large store does not scan the store's products.
SELECT p.*, ARRAY(
    SELECT c.category_id FROM product_categories c
    WHERE c.store_id = p.store_id AND c.product_id = p.id ORDER BY c.category_id)::uuid[] AS category_ids
FROM product_categories pc
JOIN products p ON p.store_id = pc.store_id AND p.id = pc.product_id
WHERE pc.store_id = @store_id AND pc.category_id = @category_id AND p.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
  AND (sqlc.narg(after)::uuid IS NULL OR pc.product_id < sqlc.narg(after)::uuid)
ORDER BY pc.product_id DESC
LIMIT @lim;

-- name: DeleteProductCategories :exec
DELETE FROM product_categories WHERE store_id = @store_id AND product_id = @product_id;

-- name: InsertProductCategories :exec
INSERT INTO product_categories (store_id, product_id, category_id)
SELECT @store_id, @product_id, c FROM unnest(@category_ids::uuid[]) AS c;

-- name: GetProductIDBySlug :one
SELECT id FROM products WHERE store_id = @store_id AND slug = @slug AND deleted_at IS NULL;

-- Storefront reads (ADR-0015): active, live products only.

-- name: GetActiveProductBySlug :one
SELECT p.*, ARRAY(
    SELECT pc.category_id FROM product_categories pc
    WHERE pc.store_id = p.store_id AND pc.product_id = p.id ORDER BY pc.category_id)::uuid[] AS category_ids
FROM products p
WHERE p.store_id = @store_id AND p.slug = @slug AND p.deleted_at IS NULL AND p.status = 'active';

-- name: ListActiveProductsByIDs :many
-- In the order given, one primary-key lookup per ID (a plain
-- `id = ANY(...)` can turn into a store-wide scan under generic plans;
-- ADR-0011). IDs that are not active, live products are skipped.
SELECT p.*, ARRAY(
    SELECT pc.category_id FROM product_categories pc
    WHERE pc.store_id = p.store_id AND pc.product_id = p.id ORDER BY pc.category_id)::uuid[] AS category_ids
FROM unnest(@ids::uuid[]) WITH ORDINALITY AS ids(id, ord)
CROSS JOIN LATERAL (
  SELECT * FROM products pr
  WHERE pr.id = ids.id AND pr.store_id = @store_id AND pr.deleted_at IS NULL AND pr.status = 'active') p
ORDER BY ids.ord;

-- name: MarkSample :execrows
-- The setup wizard's template marks the sample products it adds.
UPDATE products SET is_sample = true, updated_at = now()
WHERE store_id = @store_id AND id = @id AND deleted_at IS NULL;

-- name: ListSampleIDs :many
-- The store's sample products, for deleting them all (products_samples).
SELECT id FROM products
WHERE store_id = @store_id AND is_sample AND deleted_at IS NULL
ORDER BY id
LIMIT 200;
