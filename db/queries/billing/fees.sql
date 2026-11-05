-- name: GetFeeRule :one
SELECT rate_bps, fixed_minor, min_minor, max_minor FROM plan_fees WHERE plan_id = @plan_id AND currency = @currency;

-- name: InsertSellFee :execrows
-- Once per order: a retried or repeated delivery adds nothing.
INSERT INTO sell_fees (store_id, order_id, period, plan_id, base_minor, rate_bps, fixed_minor, fee_minor, currency, delivered_at,
  order_currency, order_base_minor, rate_micros)
VALUES (@store_id, @order_id, @period, @plan_id, @base_minor, @rate_bps, @fixed_minor, @fee_minor, @currency, @delivered_at,
  sqlc.narg(order_currency), sqlc.narg(order_base_minor), sqlc.narg(rate_micros))
ON CONFLICT (store_id, order_id) DO NOTHING;

-- name: GetSellFee :one
SELECT order_id, period, plan_id, base_minor, rate_bps, fixed_minor, fee_minor, currency, status, delivered_at, returned_at, prepaid,
  order_currency, order_base_minor, rate_micros
FROM sell_fees WHERE store_id = @store_id AND order_id = @order_id;

-- name: MarkSellFeePrepaid :exec
-- The fee was taken from the prepaid balance (ADR-0084).
UPDATE sell_fees SET prepaid = true, updated_at = now() WHERE store_id = @store_id AND order_id = @order_id;

-- name: SumPrepaidOnInvoice :one
-- What the invoice holds of the store's fees already taken from (and
-- returned to) the balance: its prepaid fees less its prepaid credits.
SELECT coalesce(sum(CASE WHEN status = 'invoiced' THEN fee_minor ELSE -fee_minor END), 0)::bigint AS net
FROM sell_fees WHERE store_id = @store_id AND invoice_id = @invoice_id AND prepaid AND status IN ('invoiced', 'credited');

-- name: SetSellFeeReturned :exec
UPDATE sell_fees SET status = @status, returned_at = @returned_at, updated_at = now()
WHERE store_id = @store_id AND order_id = @order_id;
