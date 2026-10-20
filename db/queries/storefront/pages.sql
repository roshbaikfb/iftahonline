-- name: GetPageByKey :one
SELECT * FROM store_pages WHERE store_id = @store_id AND kind = @kind AND slug = @slug;

-- name: GetPage :one
SELECT * FROM store_pages WHERE store_id = @store_id AND id = @id;

-- name: ListPageSummaries :many
-- One kind's pages without their sections or body (a landing page's
-- sections can be 64 KiB).
SELECT id, kind, slug, title, status, version, created_at, updated_at
FROM store_pages WHERE store_id = @store_id AND kind = @kind
ORDER BY slug
LIMIT 100;

-- name: CountPages :one
SELECT count(*) FROM store_pages WHERE store_id = @store_id AND kind = @kind;

-- name: InsertPage :one
-- A unique violation means the slug is taken (or, for home and policies,
-- that another save created the page first).
INSERT INTO store_pages (id, store_id, kind, slug, title, description, sections, body, status)
VALUES (@id, @store_id, @kind, @slug, @title, @description, @sections, @body, @status)
RETURNING *;

-- name: UpdatePage :one
-- Optimistic concurrency: no row back means the page is gone or its
-- version moved on.
UPDATE store_pages
SET slug = @slug, title = @title, description = @description, sections = @sections, body = @body,
    status = @status, version = version + 1, updated_at = now()
WHERE store_id = @store_id AND id = @id AND version = @version
RETURNING *;

-- name: DeletePage :one
DELETE FROM store_pages WHERE store_id = @store_id AND id = @id AND kind = 'landing'
RETURNING slug;
