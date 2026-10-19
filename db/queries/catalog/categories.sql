-- name: InsertCategory :one
INSERT INTO categories (id, store_id, parent_id, slug, name, position)
VALUES (@id, @store_id, sqlc.narg(parent_id), @slug, @name, @position)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM categories WHERE store_id = @store_id AND id = @id;

-- name: UpdateCategory :one
UPDATE categories
SET parent_id = sqlc.narg(parent_id), slug = @slug, name = @name, position = @position, updated_at = now()
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: DeleteCategory :execrows
DELETE FROM categories WHERE store_id = @store_id AND id = @id;

-- name: ListCategories :many
SELECT * FROM categories
WHERE store_id = @store_id
ORDER BY parent_id NULLS FIRST, position, name, id
LIMIT @lim;

-- name: CountCategories :one
SELECT count(*) FROM categories WHERE store_id = @store_id;

-- name: CategoryAncestors :many
-- The category and its ancestors, nearest first (bounded: depth is at
-- most domain.MaxCategoryDepth, and the bound stops a corrupt cycle).
WITH RECURSIVE up AS (
  SELECT c.id, c.parent_id, 1 AS depth FROM categories c
  WHERE c.store_id = @store_id AND c.id = @id
  UNION ALL
  SELECT c.id, c.parent_id, up.depth + 1 FROM categories c
  JOIN up ON c.id = up.parent_id
  WHERE c.store_id = @store_id AND up.depth < 16
)
SELECT id::uuid AS id FROM up ORDER BY depth;

-- name: CategorySubtreeHeight :one
-- Levels in the category's subtree, the category itself included.
WITH RECURSIVE down AS (
  SELECT c.id, 1 AS depth FROM categories c
  WHERE c.store_id = @store_id AND c.id = @id
  UNION ALL
  SELECT c.id, down.depth + 1 FROM categories c
  JOIN down ON c.parent_id = down.id
  WHERE c.store_id = @store_id AND down.depth < 16
)
SELECT coalesce(max(depth), 0)::int AS height FROM down;
