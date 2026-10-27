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

-- name: CountOrdersByStatus :many
-- Every status's number of orders, from orders_store_status alone.
SELECT status, count(*)::bigint AS n FROM orders WHERE store_id = @store_id GROUP BY status;

-- name: OrderStats :one
-- The dashboard home's sums (the owner, 2026-10-27): sales are every
-- order but cancelled and returned ones, which are summed apart; totals
-- include shipping and the COD fee. One walk of the primary key over
-- [prev_since, until): the current period and the one before it, by FILTER.
SELECT
  count(*) FILTER (WHERE created_at >= @since AND status NOT IN ('cancelled', 'returned'))::int AS orders,
  coalesce(sum(total_minor) FILTER (WHERE created_at >= @since AND status NOT IN ('cancelled', 'returned')), 0)::bigint AS sales_minor,
  count(*) FILTER (WHERE created_at >= @since AND status NOT IN ('cancelled', 'returned') AND payment_method = 'cod')::int AS cod_orders,
  count(*) FILTER (WHERE created_at >= @since AND status NOT IN ('cancelled', 'returned') AND payment_method = 'card')::int AS card_orders,
  count(*) FILTER (WHERE created_at < @prev_until AND status NOT IN ('cancelled', 'returned'))::int AS prev_orders,
  coalesce(sum(total_minor) FILTER (WHERE created_at < @prev_until AND status NOT IN ('cancelled', 'returned')), 0)::bigint AS prev_sales_minor,
  count(*) FILTER (WHERE created_at >= @since AND status = 'cancelled')::int AS cancelled_orders,
  coalesce(sum(total_minor) FILTER (WHERE created_at >= @since AND status = 'cancelled'), 0)::bigint AS cancelled_minor,
  count(*) FILTER (WHERE created_at >= @since AND status = 'returned')::int AS returned_orders,
  coalesce(sum(total_minor) FILTER (WHERE created_at >= @since AND status = 'returned'), 0)::bigint AS returned_minor
FROM orders
WHERE store_id = @store_id AND created_at >= @prev_since AND created_at < @until;

-- name: DailySales :many
-- Sales per local day for the chart, by the store's time zone; days
-- without orders are filled in by the caller.
SELECT ((created_at AT TIME ZONE @tz::text)::date)::text AS day,
       count(*)::int AS orders, coalesce(sum(total_minor), 0)::bigint AS sales_minor
FROM orders
WHERE store_id = @store_id AND created_at >= @since AND created_at < @until AND status NOT IN ('cancelled', 'returned')
GROUP BY 1
ORDER BY 1;

-- name: OldestPending :one
-- When the oldest order waiting for confirmation came (orders_store_status).
-- No row when none waits.
SELECT created_at FROM orders WHERE store_id = @store_id AND status = 'pending'
ORDER BY created_at
LIMIT 1;
