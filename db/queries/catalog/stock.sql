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
-- Sample products (ADR-0034) are never sellable.
UPDATE inventory_levels AS i
SET reserved = i.reserved + CASE WHEN i.tracked THEN @qty::int ELSE 0 END, updated_at = now()
WHERE i.store_id = @store_id AND i.variant_id = @variant_id
  AND (NOT i.tracked OR i.on_hand - i.reserved >= @qty::int)
  AND EXISTS (
    SELECT 1 FROM variants v JOIN products p ON p.store_id = v.store_id AND p.id = v.product_id
    WHERE v.store_id = i.store_id AND v.id = i.variant_id AND v.deleted_at IS NULL
      AND p.deleted_at IS NULL AND p.status = 'active' AND NOT p.is_sample)
RETURNING i.tracked, (i.on_hand - i.reserved)::int AS available,
  (SELECT v.product_id FROM variants v WHERE v.store_id = i.store_id AND v.id = i.variant_id)::uuid AS product_id;

-- name: AdjustInventory :one
-- Moves on_hand and reserved by the given deltas: a release gives back
-- reserved units (0, -qty), a commit ships them (-qty, -qty), a return
-- restocks them (+qty, 0). Returns what is available afterwards and the
-- product, so the caller can tell a variant coming back in stock
-- (ADR-0015).
UPDATE inventory_levels AS i
SET on_hand = i.on_hand + @on_hand_delta::int, reserved = i.reserved + @reserved_delta::int, updated_at = now()
WHERE i.store_id = @store_id AND i.variant_id = @variant_id
RETURNING (i.on_hand - i.reserved)::int AS available,
  (SELECT v.product_id FROM variants v WHERE v.store_id = i.store_id AND v.id = i.variant_id)::uuid AS product_id;

-- name: InsertReservations :exec
INSERT INTO stock_reservations (id, store_id, ref, variant_id, qty, counted, expires_at)
SELECT (@ids::uuid[])[n], @store_id, @ref, (@variant_ids::uuid[])[n], (@qtys::int[])[n], (@counted::boolean[])[n], sqlc.narg(expires_at)::timestamptz
FROM generate_subscripts(@ids::uuid[], 1) AS n;

-- name: ListReservations :many
SELECT * FROM stock_reservations
WHERE store_id = @store_id AND ref = @ref
ORDER BY variant_id;

-- name: MoveReservations :many
-- Moves the ref's lines from one status to the next and stamps when.
-- With only_expired, lines that have not expired yet are kept (the expiry
-- job may run early or twice); held lines (no expiry) never expire.
UPDATE stock_reservations
SET status = @to_status,
  released_at = CASE WHEN @to_status IN ('released', 'expired') THEN now() ELSE released_at END,
  committed_at = CASE WHEN @to_status = 'committed' THEN now() ELSE committed_at END,
  returned_at = CASE WHEN @to_status = 'returned' THEN now() ELSE returned_at END
WHERE store_id = @store_id AND ref = @ref AND status = @from_status
  AND (NOT @only_expired::boolean OR expires_at <= now())
RETURNING variant_id, qty, counted;

-- name: LowStock :many
-- The dashboard home's "running low": tracked variants of live, active,
-- non-sample products with at most threshold units free to sell, the
-- fewest first, with how many there are in all.
SELECT v.id AS variant_id, v.product_id, p.title AS product_title, v.option_values,
       (i.on_hand - i.reserved)::int AS available, count(*) OVER ()::int AS total
FROM inventory_levels i
JOIN variants v ON v.store_id = i.store_id AND v.id = i.variant_id AND v.deleted_at IS NULL
JOIN products p ON p.store_id = v.store_id AND p.id = v.product_id
  AND p.deleted_at IS NULL AND p.status = 'active' AND NOT p.is_sample
WHERE i.store_id = @store_id AND i.tracked AND i.on_hand - i.reserved <= @threshold::int
ORDER BY available, p.title, v.id
LIMIT @lim;
