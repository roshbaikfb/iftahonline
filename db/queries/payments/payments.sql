-- name: InsertConnection :one
INSERT INTO payment_connections (id, store_id, gateway, credentials) VALUES (@id, @store_id, @gateway, @credentials) RETURNING *;

-- name: ListConnections :many
SELECT * FROM payment_connections WHERE store_id = @store_id ORDER BY created_at DESC LIMIT 100;

-- name: GetConnection :one
SELECT * FROM payment_connections WHERE store_id = @store_id AND id = @id;

-- name: ActiveConnection :one
-- The store's active gateway account (one per gateway; the newest wins).
SELECT * FROM payment_connections WHERE store_id = @store_id AND status = 'active' ORDER BY created_at DESC LIMIT 1;

-- name: DisconnectConnection :one
UPDATE payment_connections SET status = 'disconnected', disconnected_at = now(), updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'active'
RETURNING *;

-- name: InsertPayment :one
INSERT INTO payments (id, store_id, order_id, connection_id, gateway, currency, amount_minor)
VALUES (@id, @store_id, @order_id, @connection_id, @gateway, @currency, @amount_minor)
RETURNING *;

-- name: SetCheckout :exec
UPDATE payments SET checkout_url = @checkout_url, provider_ref = @provider_ref, updated_at = now()
WHERE store_id = @store_id AND id = @id;

-- name: GetPayment :one
SELECT * FROM payments WHERE store_id = @store_id AND id = @id;

-- name: GetPaymentForUpdate :one
SELECT * FROM payments WHERE store_id = @store_id AND id = @id FOR UPDATE;

-- name: OrderPayments :many
-- An order's payments, newest first (a handful: one per checkout attempt).
SELECT * FROM payments WHERE store_id = @store_id AND order_id = @order_id ORDER BY created_at DESC LIMIT 50;

-- name: LockOrderPayments :many
-- An order's payments, locked: what the gateway's word does to them waits
-- for the caller (cancelling an unpaid order), and the other way round.
SELECT * FROM payments WHERE store_id = @store_id AND order_id = @order_id ORDER BY id LIMIT 50 FOR UPDATE;

-- name: ListPayments :many
SELECT * FROM payments
WHERE store_id = @store_id AND (created_at, id) < (@before_at::timestamptz, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: SetPaymentState :one
UPDATE payments SET status = @status, refunded_minor = @refunded_minor, transaction_id = @transaction_id, failure = @failure,
  status_at = CASE WHEN status <> @status THEN now() ELSE status_at END, updated_at = now(), version = version + 1
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: InsertRefund :one
INSERT INTO payment_refunds (id, store_id, payment_id, currency, amount_minor, reason)
VALUES (@id, @store_id, @payment_id, @currency, @amount_minor, @reason)
RETURNING *;

-- name: GetRefundForUpdate :one
SELECT * FROM payment_refunds WHERE store_id = @store_id AND id = @id FOR UPDATE;

-- name: PaymentRefunds :many
SELECT * FROM payment_refunds WHERE store_id = @store_id AND payment_id = @payment_id ORDER BY created_at LIMIT 100;

-- name: RequestedRefunds :one
-- What refunds asked for and not yet answered would take off a payment.
SELECT coalesce(sum(amount_minor), 0)::bigint AS minor FROM payment_refunds
WHERE store_id = @store_id AND payment_id = @payment_id AND status = 'requested';

-- name: SetRefund :exec
UPDATE payment_refunds SET status = @status, provider_refund_id = @provider_refund_id, failure = @failure, updated_at = now()
WHERE store_id = @store_id AND id = @id;
