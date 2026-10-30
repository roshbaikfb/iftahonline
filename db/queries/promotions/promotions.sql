-- name: InsertCoupon :one
INSERT INTO coupons (store_id, id, code, kind, percent, amount_minor, min_subtotal_minor, max_uses, max_per_customer,
  starts_at, ends_at, active)
VALUES (@store_id, @id, @code, @kind, @percent, @amount_minor, @min_subtotal_minor, sqlc.narg(max_uses), sqlc.narg(max_per_customer),
  sqlc.narg(starts_at), sqlc.narg(ends_at), @active)
RETURNING *;

-- name: UpdateCoupon :one
UPDATE coupons
SET code = @code, kind = @kind, percent = @percent, amount_minor = @amount_minor, min_subtotal_minor = @min_subtotal_minor,
    max_uses = sqlc.narg(max_uses), max_per_customer = sqlc.narg(max_per_customer), starts_at = sqlc.narg(starts_at),
    ends_at = sqlc.narg(ends_at), active = @active, updated_at = now()
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: GetCoupon :one
SELECT * FROM coupons WHERE store_id = @store_id AND id = @id;

-- name: ListCoupons :many
-- A page of the store's coupons, newest first (IDs are ULIDs: their
-- order is the coupons' creation), after a keyset cursor; the primary key
-- serves it.
SELECT * FROM coupons WHERE store_id = @store_id AND id < @before ORDER BY id DESC LIMIT @lim;

-- name: CouponByCode :one
SELECT * FROM coupons WHERE store_id = @store_id AND code = @code;

-- name: LockCustomerUses :exec
-- One customer's orders take a coupon one at a time until the order's
-- transaction ends (its per-customer limit); other customers wait for
-- nothing. The key is the store, coupon and customer.
SELECT pg_advisory_xact_lock(hashtextextended(CAST(@store_id::uuid AS text) || ':' || CAST(@coupon_id::uuid AS text) || ':' || CAST(@customer_id::uuid AS text), 0));

-- name: DeleteUnusedCoupon :execrows
-- Only a coupon no order ever held.
DELETE FROM coupons c WHERE c.store_id = @store_id AND c.id = @id
  AND NOT EXISTS (SELECT 1 FROM coupon_redemptions r WHERE r.store_id = c.store_id AND r.coupon_id = c.id);

-- name: CustomerUses :one
SELECT count(*)::int FROM coupon_redemptions
WHERE store_id = @store_id AND coupon_id = @coupon_id AND customer_id = @customer_id AND released_at IS NULL;

-- name: InsertRedemption :execrows
INSERT INTO coupon_redemptions (store_id, order_id, coupon_id, customer_id, discount_minor, free_shipping)
VALUES (@store_id, @order_id, @coupon_id, @customer_id, @discount_minor, @free_shipping)
ON CONFLICT (store_id, order_id) DO NOTHING;

-- name: ConsumeUse :one
-- Takes a use while one is left and the coupon is on. The row is locked
-- only from here to the order's commit, its last step; a concurrent
-- order waits, then reads the count it left (no row: used up).
UPDATE coupons SET uses = uses + 1, updated_at = now()
WHERE store_id = @store_id AND id = @id AND active AND (max_uses IS NULL OR uses < max_uses)
RETURNING uses;

-- name: ReleaseRedemption :one
-- An order's use comes back once.
UPDATE coupon_redemptions SET released_at = now()
WHERE store_id = @store_id AND order_id = @order_id AND released_at IS NULL
RETURNING coupon_id;

-- name: UncountUse :exec
UPDATE coupons SET uses = greatest(uses - 1, 0), updated_at = now() WHERE store_id = @store_id AND id = @id;
