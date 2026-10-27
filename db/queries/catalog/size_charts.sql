-- name: InsertSizeChart :one
INSERT INTO size_charts (id, store_id, name, header, body)
VALUES (@id, @store_id, @name, @header, @body)
RETURNING *;

-- name: UpdateSizeChart :one
UPDATE size_charts SET name = @name, header = @header, body = @body, updated_at = now()
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: GetSizeChart :one
SELECT * FROM size_charts WHERE store_id = @store_id AND id = @id;

-- name: DeleteSizeChart :execrows
-- Its products lose the chart (products_size_chart_fkey sets it NULL).
DELETE FROM size_charts WHERE store_id = @store_id AND id = @id;

-- name: ListSizeCharts :many
-- By name, each with how many live products use it (products_size_chart).
SELECT sc.*, (
    SELECT count(*) FROM products p
    WHERE p.store_id = sc.store_id AND p.size_chart_id = sc.id AND p.deleted_at IS NULL)::int AS products
FROM size_charts sc
WHERE sc.store_id = @store_id
ORDER BY sc.name, sc.id
LIMIT @lim;

-- name: CountSizeCharts :one
SELECT count(*) FROM size_charts WHERE store_id = @store_id;

-- name: CountSizeChartProducts :one
SELECT count(*)::int FROM products
WHERE store_id = @store_id AND size_chart_id = @id AND deleted_at IS NULL;
