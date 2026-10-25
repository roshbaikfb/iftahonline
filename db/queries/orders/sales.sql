-- Bestsellers (ADR-0033).

-- name: IsSaleCounted :one
SELECT EXISTS (SELECT 1 FROM order_sales_counted WHERE store_id = @store_id AND order_id = @order_id) AS counted;

-- name: MarkSaleCounted :exec
INSERT INTO order_sales_counted (store_id, order_id) VALUES (@store_id, @order_id);

-- name: UnmarkSaleCounted :exec
DELETE FROM order_sales_counted WHERE store_id = @store_id AND order_id = @order_id;

-- name: AddProductSales :exec
-- Adds an order's units to its products on its day (one entry per
-- product).
INSERT INTO product_sales (store_id, product_id, day, units)
SELECT @store_id, unnest(@product_ids::uuid[]), @day, unnest(@units::int[])
ON CONFLICT (store_id, product_id, day) DO UPDATE SET units = product_sales.units + EXCLUDED.units;

-- name: SubtractProductSales :exec
-- Takes an order's units back from its products on its day, never below
-- zero (a day pruned meanwhile has nothing to take back).
UPDATE product_sales s SET units = GREATEST(s.units - p.units, 0)
FROM (SELECT unnest(@product_ids::uuid[]) AS product_id, unnest(@units::int[]) AS units) AS p
WHERE s.store_id = @store_id AND s.day = @day AND s.product_id = p.product_id;

-- name: TopSellingProducts :many
-- The store's products by units sold since a day, most first.
SELECT product_id, sum(units)::bigint AS units
FROM product_sales
WHERE store_id = @store_id AND day >= @since
GROUP BY product_id
HAVING sum(units) > 0
ORDER BY units DESC, product_id
LIMIT @lim;

-- name: PruneProductSales :exec
-- Days no read reaches any more.
DELETE FROM product_sales WHERE store_id = @store_id AND day < @before;
