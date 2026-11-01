-- The analytics module's queries (ADR-0069), all on the store in
-- context (RLS), except the two store lists of the scheduled jobs.

-- name: OrderFactDay :many
-- The day an order's fact had, if any (to mark it dirty when it moves).
SELECT day FROM analytics_orders WHERE store_id = @store_id AND order_id = @order_id;

-- name: UpsertOrderFact :exec
INSERT INTO analytics_orders (store_id, order_id, day, state, governorate, campaign, currency, revenue_minor, shipping_minor,
                              cod_fee_minor, on_delivery_minor, deposit_paid_minor, risk_band, updated_at)
VALUES (@store_id, @order_id, @day, @state, @governorate, @campaign, @currency, @revenue_minor, @shipping_minor,
        @cod_fee_minor, @on_delivery_minor, @deposit_paid_minor, sqlc.narg(risk_band), now())
ON CONFLICT (store_id, order_id) DO UPDATE
SET day = excluded.day, state = excluded.state, governorate = excluded.governorate, campaign = excluded.campaign,
    revenue_minor = excluded.revenue_minor, shipping_minor = excluded.shipping_minor, cod_fee_minor = excluded.cod_fee_minor,
    on_delivery_minor = excluded.on_delivery_minor, deposit_paid_minor = excluded.deposit_paid_minor,
    risk_band = excluded.risk_band, updated_at = now();

-- name: DeleteOrderLines :exec
DELETE FROM analytics_lines WHERE store_id = @store_id AND order_id = @order_id;

-- name: InsertOrderLines :exec
INSERT INTO analytics_lines (store_id, order_id, line, product_id, title, qty, revenue_minor, cost_minor)
SELECT @store_id, @order_id, n - 1, (@product_ids::uuid[])[n], (@titles::text[])[n], (@qtys::int[])[n],
       (@revenues::bigint[])[n], nullif((@costs::bigint[])[n], -1)
FROM generate_subscripts(@product_ids::uuid[], 1) AS n;

-- name: MarkDirty :exec
INSERT INTO analytics_dirty (store_id, day)
SELECT @store_id, d FROM unnest(@days::date[]) AS d
ON CONFLICT DO NOTHING;

-- name: MarkAllDirty :exec
-- Every day with orders or ad spend: the estimates changed.
INSERT INTO analytics_dirty (store_id, day)
SELECT DISTINCT @store_id::uuid, day FROM (
  SELECT day FROM analytics_orders WHERE store_id = @store_id
  UNION SELECT day FROM ad_spend WHERE store_id = @store_id) d
ON CONFLICT DO NOTHING;

-- name: TakeDirty :many
-- Up to @max_days dirty days, removed as they are taken (the rollup's
-- transaction recomputes them).
DELETE FROM analytics_dirty AS d
WHERE d.store_id = @store_id AND d.day IN (
  SELECT x.day FROM analytics_dirty AS x WHERE x.store_id = @store_id ORDER BY x.day LIMIT @max_days)
RETURNING d.day;

-- name: DayOrders :many
SELECT order_id, state, governorate, campaign, currency, revenue_minor, shipping_minor, cod_fee_minor, on_delivery_minor,
       deposit_paid_minor, carrier_charge_minor
FROM analytics_orders WHERE store_id = @store_id AND day = @day
ORDER BY order_id;

-- name: DayLines :many
SELECT l.order_id, l.product_id, l.title, l.qty, l.revenue_minor, l.cost_minor
FROM analytics_lines l
JOIN analytics_orders o ON o.store_id = l.store_id AND o.order_id = l.order_id
WHERE o.store_id = @store_id AND o.day = @day
ORDER BY l.order_id, l.line;

-- name: DayAdSpend :many
SELECT campaign, sum(amount_minor)::bigint AS amount_minor
FROM ad_spend WHERE store_id = @store_id AND day = @day
GROUP BY campaign;

-- name: DeleteDay :exec
DELETE FROM profit_daily WHERE store_id = @store_id AND day = @day;

-- name: InsertDayRows :exec
INSERT INTO profit_daily (store_id, dim, day, key, label, currency, orders, delivered, returned, cancelled, open, cost_missing,
                          revenue_minor, delivery_paid_minor, product_cost_minor, shipping_cost_minor, cod_fees_minor,
                          return_cost_minor, ad_spend_minor)
SELECT @store_id, (@dims::text[])[n], @day, (@keys::text[])[n], (@labels::text[])[n], @currency,
       (@orders::int[])[n], (@delivered::int[])[n], (@returned::int[])[n], (@cancelled::int[])[n], (@open::int[])[n],
       (@cost_missing::int[])[n], (@revenues::bigint[])[n], (@delivery_paid::bigint[])[n], (@product_costs::bigint[])[n],
       (@shipping_costs::bigint[])[n], (@cod_fees::bigint[])[n], (@return_costs::bigint[])[n], (@ad_spend::bigint[])[n]
FROM generate_subscripts(@dims::text[], 1) AS n;

-- name: Uncharged :many
-- Settled-state orders of the last 60 days whose carrier charge is not
-- known yet, a batch at a time.
SELECT order_id, day FROM analytics_orders
WHERE store_id = @store_id AND carrier_charge_minor IS NULL AND state IN ('delivered', 'returned') AND day >= @since
ORDER BY day, order_id
LIMIT @max_orders OFFSET @skip;

-- name: SetCarrierCharges :exec
UPDATE analytics_orders AS o SET carrier_charge_minor = c.charge, updated_at = now()
FROM (SELECT unnest(@order_ids::uuid[]) AS order_id, unnest(@charges::bigint[]) AS charge) c
WHERE o.store_id = @store_id AND o.order_id = c.order_id;

-- name: ProfitSettings :many
SELECT delivery_cost_minor, return_cost_minor, cod_fee_bp FROM profit_settings WHERE store_id = @store_id;

-- name: SaveProfitSettings :exec
INSERT INTO profit_settings (store_id, delivery_cost_minor, return_cost_minor, cod_fee_bp)
VALUES (@store_id, sqlc.narg(delivery_cost_minor), sqlc.narg(return_cost_minor), @cod_fee_bp)
ON CONFLICT (store_id) DO UPDATE
SET delivery_cost_minor = excluded.delivery_cost_minor, return_cost_minor = excluded.return_cost_minor,
    cod_fee_bp = excluded.cod_fee_bp, updated_at = now();

-- name: InsertAdSpend :one
INSERT INTO ad_spend (id, store_id, day, platform, campaign, currency, amount_minor, source, note)
VALUES (@id, @store_id, @day, @platform, @campaign, @currency, @amount_minor, @source, @note)
RETURNING *;

-- name: DeleteImportedAdSpend :exec
-- An import replaces what the platform said of [from, to) before.
DELETE FROM ad_spend
WHERE store_id = @store_id AND source = 'import' AND platform = @platform AND day >= @from_day AND day < @to_day;

-- name: DeleteAdSpend :one
DELETE FROM ad_spend WHERE store_id = @store_id AND id = @id RETURNING day;

-- name: ListAdSpend :many
SELECT * FROM ad_spend WHERE store_id = @store_id AND day >= @from_day AND day < @to_day
ORDER BY day DESC, id DESC
LIMIT 500;

-- name: AdSpendTotals :one
-- Every entry of the range, beyond the list's 500.
SELECT count(*)::int AS entries, coalesce(sum(amount_minor), 0)::bigint AS amount_minor
FROM ad_spend WHERE store_id = @store_id AND day >= @from_day AND day < @to_day;

-- name: ProfitTotals :one
-- The dashboard's totals over [from, to), read on the read pool.
SELECT coalesce(sum(orders), 0)::int AS orders, coalesce(sum(delivered), 0)::int AS delivered,
       coalesce(sum(returned), 0)::int AS returned, coalesce(sum(cancelled), 0)::int AS cancelled,
       coalesce(sum(open), 0)::int AS open, coalesce(sum(cost_missing), 0)::int AS cost_missing,
       coalesce(sum(revenue_minor), 0)::bigint AS revenue_minor, coalesce(sum(delivery_paid_minor), 0)::bigint AS delivery_paid_minor,
       coalesce(sum(product_cost_minor), 0)::bigint AS product_cost_minor, coalesce(sum(shipping_cost_minor), 0)::bigint AS shipping_cost_minor,
       coalesce(sum(cod_fees_minor), 0)::bigint AS cod_fees_minor, coalesce(sum(return_cost_minor), 0)::bigint AS return_cost_minor,
       coalesce(sum(ad_spend_minor), 0)::bigint AS ad_spend_minor
FROM profit_daily WHERE store_id = @store_id AND dim = 'total' AND day >= @from_day AND day < @to_day;

-- name: ProfitDays :many
-- The totals day by day, for the chart.
SELECT day, revenue_minor, delivery_paid_minor, product_cost_minor, shipping_cost_minor, cod_fees_minor, return_cost_minor, ad_spend_minor,
       orders
FROM profit_daily WHERE store_id = @store_id AND dim = 'total' AND day >= @from_day AND day < @to_day
ORDER BY day;

-- name: ProfitBreakdown :many
-- One dimension's keys over [from, to), at most 200, most profitable
-- first.
SELECT key, max(label)::text AS label,
       sum(orders)::int AS orders, sum(delivered)::int AS delivered, sum(returned)::int AS returned,
       sum(cancelled)::int AS cancelled, sum(open)::int AS open, sum(cost_missing)::int AS cost_missing,
       sum(revenue_minor)::bigint AS revenue_minor, sum(delivery_paid_minor)::bigint AS delivery_paid_minor,
       sum(product_cost_minor)::bigint AS product_cost_minor, sum(shipping_cost_minor)::bigint AS shipping_cost_minor,
       sum(cod_fees_minor)::bigint AS cod_fees_minor, sum(return_cost_minor)::bigint AS return_cost_minor,
       sum(ad_spend_minor)::bigint AS ad_spend_minor
FROM profit_daily WHERE store_id = @store_id AND dim = @dim AND day >= @from_day AND day < @to_day
GROUP BY key
ORDER BY sum(revenue_minor + delivery_paid_minor - product_cost_minor - shipping_cost_minor - cod_fees_minor
             - return_cost_minor - ad_spend_minor) DESC, key
LIMIT 200;

-- name: DirtyStores :many
SELECT s::uuid AS store_id FROM app.analytics_dirty_stores() AS s;

-- name: SweepStores :many
SELECT s::uuid AS store_id FROM app.analytics_sweep_stores() AS s;

-- name: QueueBackfill :one
INSERT INTO analytics_backfills (store_id) VALUES (@store_id) ON CONFLICT (store_id) DO NOTHING RETURNING store_id;
