-- Order lists, newest first (ADR-0021). Each query is driven by the index
-- of its most selective filter; the others are applied inside that walk.
-- The created_at range and the keyset bound are always set (all of time
-- and the largest ID on the first page), so partitions are pruned and
-- every page is an index range, however deep.

-- name: ListOrders :many
SELECT * FROM orders
WHERE store_id = @store_id AND created_at >= @from_at AND created_at < @to_at AND id < @before_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(governorate)::text IS NULL OR governorate = sqlc.narg(governorate)::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListOrdersByStatus :many
SELECT * FROM orders
WHERE store_id = @store_id AND status = @status AND created_at >= @from_at AND created_at < @to_at AND id < @before_id
  AND (sqlc.narg(governorate)::text IS NULL OR governorate = sqlc.narg(governorate)::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListOrdersByPhone :many
SELECT * FROM orders
WHERE store_id = @store_id AND phone = @phone AND created_at >= @from_at AND created_at < @to_at AND id < @before_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(governorate)::text IS NULL OR governorate = sqlc.narg(governorate)::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListOrdersByCustomer :many
SELECT * FROM orders
WHERE store_id = @store_id AND customer_id = @customer_id AND created_at >= @from_at AND created_at < @to_at AND id < @before_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(governorate)::text IS NULL OR governorate = sqlc.narg(governorate)::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;
