-- Every lookup of one order names its partition key: created_at is the
-- time its ULID encodes (domain.CreatedAtOf), so it reaches one partition.

-- name: InsertOrder :exec
INSERT INTO orders (id, store_id, created_at, source, customer_id, name, phone, governorate, city, address, notes,
                    currency, subtotal_minor, shipping_minor, cod_fee_minor, total_minor, item_count, lines_hash, updated_at, status_changed_at)
VALUES (@id, @store_id, @created_at, @source, @customer_id, @name, @phone, @governorate, @city, @address, @notes,
        @currency, @subtotal_minor, @shipping_minor, @cod_fee_minor, @total_minor, @item_count, @lines_hash, @created_at, @created_at);

-- name: InsertOrderItems :exec
INSERT INTO order_items (store_id, order_created_at, order_id, line, variant_id, product_id, title, variant_title, sku,
                         qty, unit_price_minor, total_minor)
SELECT @store_id, @order_created_at, @order_id, n, (@variant_ids::uuid[])[n], (@product_ids::uuid[])[n],
       (@titles::text[])[n], (@variant_titles::text[])[n], (@skus::text[])[n], (@qtys::int[])[n],
       (@unit_prices::bigint[])[n], (@totals::bigint[])[n]
FROM generate_subscripts(@variant_ids::uuid[], 1) AS n;

-- name: InsertOrderEvent :exec
INSERT INTO order_events (id, store_id, order_created_at, order_id, occurred_at, from_status, to_status, actor_user_id, actor, note)
VALUES (@id, @store_id, @order_created_at, @order_id, @occurred_at, sqlc.narg(from_status), @to_status, sqlc.narg(actor_user_id), @actor, @note);

-- name: GetOrder :one
SELECT * FROM orders WHERE store_id = @store_id AND created_at = @created_at AND id = @id;

-- name: GetOrderForUpdate :one
-- Locks the order for a transition: concurrent moves of one order
-- serialize, and each sees the status the other left.
SELECT * FROM orders WHERE store_id = @store_id AND created_at = @created_at AND id = @id FOR UPDATE;

-- name: SetOrderStatus :one
UPDATE orders
SET status = @to_status, version = version + 1, updated_at = @at, status_changed_at = @at
WHERE store_id = @store_id AND created_at = @created_at AND id = @id
RETURNING *;

-- name: ListOrderItems :many
SELECT * FROM order_items
WHERE store_id = @store_id AND order_created_at = @order_created_at AND order_id = @order_id
ORDER BY line;

-- name: ListOrderEvents :many
SELECT * FROM order_events
WHERE store_id = @store_id AND order_created_at = @order_created_at AND order_id = @order_id
ORDER BY occurred_at, id;

-- name: ListItemsOfOrders :many
-- The lines of a page of orders (CSV export) in one query: a LATERAL
-- lookup per order on the primary key, each pruned to its partition.
SELECT i.* FROM generate_subscripts(@order_ids::uuid[], 1) AS n
CROSS JOIN LATERAL (
  SELECT * FROM order_items x
  WHERE x.store_id = @store_id AND x.order_created_at = (@created_ats::timestamptz[])[n]
    AND x.order_id = (@order_ids::uuid[])[n]
  ORDER BY x.line LIMIT 50) AS i
ORDER BY n, i.line;

-- name: FindDuplicateOrder :one
-- The duplicate-order window: the same phone ordering the same lines
-- again soon is a double submit, not a second order.
SELECT id FROM orders
WHERE store_id = @store_id AND phone = @phone AND created_at >= @since AND lines_hash = @lines_hash
  AND status <> 'cancelled'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: LockOrderPhone :exec
-- Serializes concurrent orders by one phone in one store (the key is a
-- hash of both), so the duplicate check above cannot race.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: InsertIdempotencyKey :one
-- No row back means the key exists: another request (perhaps still
-- running, which this waits for) created its order.
INSERT INTO order_idempotency (store_id, key, fingerprint, order_id)
VALUES (@store_id, @key, @fingerprint, @order_id)
ON CONFLICT (store_id, key) DO NOTHING
RETURNING order_id;

-- name: GetIdempotencyKey :one
SELECT * FROM order_idempotency WHERE store_id = @store_id AND key = @key;

-- name: PurgeIdempotencyKeys :exec
-- A keyed order deletes a few of its store's expired keys.
DELETE FROM order_idempotency AS d
WHERE d.store_id = @store_id AND d.key IN (
  SELECT k.key FROM order_idempotency k
  WHERE k.store_id = @store_id AND k.created_at < @before
  ORDER BY k.created_at LIMIT 20);
