-- name: GetCartByToken :one
SELECT id FROM carts WHERE store_id = @store_id AND token_hash = @token_hash AND expires_at > now();

-- name: InsertCart :exec
INSERT INTO carts (id, store_id, token_hash, expires_at) VALUES (@id, @store_id, @token_hash, @expires_at);

-- name: TouchCart :exec
UPDATE carts SET expires_at = @expires_at, updated_at = now() WHERE store_id = @store_id AND id = @id;

-- name: ListCartItems :many
SELECT variant_id, qty FROM cart_items WHERE store_id = @store_id AND cart_id = @cart_id
ORDER BY added_at, variant_id
LIMIT 50;

-- name: CountCartLines :one
SELECT count(*) FROM cart_items WHERE store_id = @store_id AND cart_id = @cart_id;

-- name: AddCartItem :one
-- Adding a variant already in the cart adds to its quantity, capped.
INSERT INTO cart_items (store_id, cart_id, variant_id, qty) VALUES (@store_id, @cart_id, @variant_id, @qty)
ON CONFLICT (store_id, cart_id, variant_id) DO UPDATE SET qty = LEAST(cart_items.qty + EXCLUDED.qty, 20)
RETURNING qty;

-- name: SetCartItem :execrows
UPDATE cart_items SET qty = @qty WHERE store_id = @store_id AND cart_id = @cart_id AND variant_id = @variant_id;

-- name: DeleteCartItem :exec
DELETE FROM cart_items WHERE store_id = @store_id AND cart_id = @cart_id AND variant_id = @variant_id;

-- name: CartQuantity :one
-- Total units in the cart that the token opens: the header's badge.
SELECT coalesce(sum(ci.qty), 0)::int
FROM carts c JOIN cart_items ci ON ci.store_id = c.store_id AND ci.cart_id = c.id
WHERE c.store_id = @store_id AND c.token_hash = @token_hash AND c.expires_at > now();
