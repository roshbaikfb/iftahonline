-- name: RecordOrder :one
-- A new order from phone: creates the customer or updates the latest
-- name and the order count. The caller's transaction rolls it back with
-- the order when the customer is blocked.
INSERT INTO customers (id, store_id, phone, name, name_key, orders_count, last_order_at)
VALUES (@id, @store_id, @phone, @name, @name_key, 1, now())
ON CONFLICT (store_id, phone) DO UPDATE
SET name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE customers.name END,
    name_key = CASE WHEN excluded.name <> '' THEN excluded.name_key ELSE customers.name_key END,
    orders_count = customers.orders_count + 1,
    last_order_at = now(), updated_at = now()
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customers WHERE store_id = @store_id AND id = @id;

-- name: GetCustomerByPhone :one
SELECT * FROM customers WHERE store_id = @store_id AND phone = @phone;

-- name: ListCustomers :many
-- Newest first, keyset on id: before_id is always a bound (the caller
-- passes the largest UUID for the first page), so every page is an index
-- range of customers_store_list, however deep. A phone matches exactly;
-- a name matches as a prefix of any word's search form, filtered inside
-- that walk and bounded by the limit.
SELECT * FROM customers
WHERE store_id = @store_id AND id < @before_id
  AND (NOT @only_blocked::boolean OR blocked_at IS NOT NULL)
  AND (sqlc.narg(phone)::text IS NULL OR phone = sqlc.narg(phone)::text)
  AND (sqlc.narg(name_prefix)::text IS NULL
       OR name_key LIKE sqlc.narg(name_prefix)::text || '%'
       OR name_key LIKE '% ' || sqlc.narg(name_prefix)::text || '%')
ORDER BY id DESC
LIMIT @lim;

-- name: BlockPhone :one
-- Blocks a phone, creating its customer if it never ordered. Blocking
-- again keeps the first block time and takes the new reason.
INSERT INTO customers (id, store_id, phone, blocked_at, block_reason)
VALUES (@id, @store_id, @phone, now(), @reason)
ON CONFLICT (store_id, phone) DO UPDATE
SET blocked_at = coalesce(customers.blocked_at, now()), block_reason = excluded.block_reason, updated_at = now()
RETURNING *;

-- name: BlockCustomer :one
UPDATE customers
SET blocked_at = coalesce(blocked_at, now()), block_reason = @reason, updated_at = now()
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: UnblockCustomer :one
UPDATE customers
SET blocked_at = NULL, block_reason = '', updated_at = now()
WHERE store_id = @store_id AND id = @id
RETURNING *;
