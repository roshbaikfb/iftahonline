-- name: InsertImport :one
INSERT INTO catalog_imports (id, store_id, kind, source_key, woo_url, woo_credentials)
VALUES (@id, @store_id, @kind, sqlc.narg(source_key), sqlc.narg(woo_url), sqlc.narg(woo_credentials))
RETURNING *;

-- name: GetImport :one
SELECT * FROM catalog_imports WHERE store_id = @store_id AND id = @id;

-- name: ListImports :many
SELECT * FROM catalog_imports WHERE store_id = @store_id ORDER BY id DESC LIMIT @lim;

-- name: QueueImport :one
UPDATE catalog_imports SET status = 'queued', updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'pending'
RETURNING *;

-- name: StartImport :one
UPDATE catalog_imports
SET status = 'running', total = @total, started_at = coalesce(started_at, now()), updated_at = now()
WHERE store_id = @store_id AND id = @id AND status IN ('queued', 'running')
RETURNING *;

-- name: SaveImportProgress :exec
UPDATE catalog_imports
SET cursor = @cursor, created_count = @created_count, updated_count = @updated_count,
    failed_count = @failed_count, row_errors = @row_errors, updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'running';

-- name: FinishImport :one
UPDATE catalog_imports
SET status = @status, failure_reason = @failure_reason, finished_at = now(), updated_at = now()
WHERE store_id = @store_id AND id = @id AND status IN ('queued', 'running')
RETURNING *;
