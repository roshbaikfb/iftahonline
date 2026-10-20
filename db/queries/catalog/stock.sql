-- name: SetInventoryLevel :one
-- Fails the inventory_levels_bounds CHECK when on_hand < reserved.
UPDATE inventory_levels AS i
SET on_hand = @on_hand, tracked = @tracked, updated_at = now()
FROM variants v
WHERE i.store_id = @store_id AND i.variant_id = @variant_id
  AND v.store_id = i.store_id AND v.id = i.variant_id AND v.deleted_at IS NULL
RETURNING i.variant_id, i.tracked, i.on_hand, i.reserved, v.product_id;

-- name: ReserveInventory :one
-- The race-safe step (ADR-0011): the row lock serializes concurrent
-- reservations of one variant, and the WHERE re-checks availability on
-- the locked row. No row means the variant is not sellable or short.
UPDATE inventory_levels AS i
SET reserved = i.reserved + CASE WHEN i.tracked THEN @qty::int ELSE 0 END, updated_at = now()
WHERE i.store_id = @store_id AND i.variant_id = @variant_id
  AND (NOT i.tracked OR i.on_hand - i.reserved >= @qty::int)
  AND EXISTS (
    SELECT 1 FROM variants v JOIN products p ON p.store_id = v.store_id AND p.id = v.product_id
    WHERE v.store_id = i.store_id AND v.id = i.variant_id AND v.deleted_at IS NULL
      AND p.deleted_at IS NULL AND p.status = 'active')
RETURNING i.tracked, (i.on_hand - i.reserved)::int AS available,
  (SELECT v.product_id FROM variants v WHERE v.store_id = i.store_id AND v.id = i.variant_id)::uuid AS product_id;

-- name: UnreserveInventory :one
-- Returns what is available afterwards and the product, so the caller
-- can tell a variant coming back in stock (ADR-0015).
UPDATE inventory_levels AS i SET reserved = i.reserved - @qty::int, updated_at = now()
WHERE i.store_id = @store_id AND i.variant_id = @variant_id
RETURNING (i.on_hand - i.reserved)::int AS available,
  (SELECT v.product_id FROM variants v WHERE v.store_id = i.store_id AND v.id = i.variant_id)::uuid AS product_id;

-- name: InsertReservations :exec
INSERT INTO stock_reservations (id, store_id, ref, variant_id, qty, counted, expires_at)
SELECT (@ids::uuid[])[n], @store_id, @ref, (@variant_ids::uuid[])[n], (@qtys::int[])[n], (@counted::boolean[])[n], @expires_at
FROM generate_subscripts(@ids::uuid[], 1) AS n;

-- name: ListReservations :many
SELECT * FROM stock_reservations
WHERE store_id = @store_id AND ref = @ref
ORDER BY variant_id;

-- name: EndReservations :many
-- Ends the ref's active lines. With only_expired, lines that have not
-- expired yet are kept (the expiry job may run early or twice).
UPDATE stock_reservations
SET status = @status, released_at = now()
WHERE store_id = @store_id AND ref = @ref AND status = 'active'
  AND (NOT @only_expired::boolean OR expires_at <= now())
RETURNING variant_id, qty, counted;
