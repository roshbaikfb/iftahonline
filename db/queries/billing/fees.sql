-- name: GetFeeRule :one
SELECT rate_bps, min_minor, max_minor FROM plan_fees WHERE plan_id = @plan_id AND currency = @currency;

-- name: InsertSellFee :execrows
-- Once per order: a retried or repeated delivery adds nothing.
INSERT INTO sell_fees (store_id, order_id, period, plan_id, base_minor, rate_bps, fee_minor, currency, delivered_at)
VALUES (@store_id, @order_id, @period, @plan_id, @base_minor, @rate_bps, @fee_minor, @currency, @delivered_at)
ON CONFLICT (store_id, order_id) DO NOTHING;

-- name: GetSellFee :one
SELECT order_id, period, plan_id, base_minor, rate_bps, fee_minor, currency, status, delivered_at, returned_at
FROM sell_fees WHERE store_id = @store_id AND order_id = @order_id;

-- name: SetSellFeeReturned :exec
UPDATE sell_fees SET status = @status, returned_at = @returned_at, updated_at = now()
WHERE store_id = @store_id AND order_id = @order_id;
