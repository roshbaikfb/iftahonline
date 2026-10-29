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
-- The store's coupons, newest first (a store keeps tens, not thousands).
SELECT * FROM coupons WHERE store_id = @store_id ORDER BY created_at DESC, id DESC LIMIT 500;

-- name: CouponByCode :one
SELECT * FROM coupons WHERE store_id = @store_id AND code = @code;

-- name: LockCouponByCode :one
-- The order's transaction holds the coupon until it commits: concurrent
-- orders take its uses one at a time.
SELECT * FROM coupons WHERE store_id = @store_id AND code = @code FOR UPDATE;

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

-- name: CountUse :exec
UPDATE coupons SET uses = uses + 1, updated_at = now() WHERE store_id = @store_id AND id = @id;

-- name: ReleaseRedemption :one
-- An order's use comes back once.
UPDATE coupon_redemptions SET released_at = now()
WHERE store_id = @store_id AND order_id = @order_id AND released_at IS NULL
RETURNING coupon_id;

-- name: UncountUse :exec
UPDATE coupons SET uses = greatest(uses - 1, 0), updated_at = now() WHERE store_id = @store_id AND id = @id;
