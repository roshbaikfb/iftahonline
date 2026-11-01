-- P8 review (R3): the second half of P8's expand-only changes.
--
-- * The CHECKs P8 added NOT VALID (00072, 00074, 00076, 00077, 00078) are
--   validated. VALIDATE takes SHARE UPDATE EXCLUSIVE: reads and writes go
--   on while it scans, and every row written since was checked already.
-- * app.risk_signals answers only inside a store's transaction
--   (app.store_id set). Its keys stay any phone's: the order path scores
--   a new customer's phone before the order exists. A key is an HMAC
--   under RISK_PHONE_KEY, so it cannot be made up (ADR-0065).
-- * The daily sweep of carrier charges lists its stores by day: a
--   partial index on (day, store_id), built without blocking writes. On
--   2 million orders of 5,000 stores its plan went from a scan of every
--   uncharged order (42 ms) to an index-only scan of the last 60 days
--   (4 ms).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE orders VALIDATE CONSTRAINT orders_outcome_reason;
ALTER TABLE orders VALIDATE CONSTRAINT orders_risk_action;
ALTER TABLE orders VALIDATE CONSTRAINT orders_risk_band;
ALTER TABLE orders VALIDATE CONSTRAINT orders_deposit;
ALTER TABLE orders VALIDATE CONSTRAINT orders_deposit_status;
ALTER TABLE risk_settings VALIDATE CONSTRAINT risk_settings_prepay_from;
ALTER TABLE variants VALIDATE CONSTRAINT variants_cost_minor;
ALTER TABLE order_items VALIDATE CONSTRAINT order_items_unit_cost;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.risk_signals(p_keys bytea[])
  RETURNS TABLE (phone_key bytea, outcome text, outcome_at timestamptz, store_no int)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT k.key, r.outcome, r.outcome_at, dense_rank() OVER (PARTITION BY k.key ORDER BY r.store_id)::int
  FROM (SELECT DISTINCT key FROM unnest(p_keys[1:100]) AS u(key) WHERE app.current_store_id() IS NOT NULL) k
  CROSS JOIN LATERAL (
    SELECT o.store_id, o.outcome, o.outcome_at FROM public.risk_orders o
    WHERE o.phone_key = k.key AND o.outcome IS NOT NULL AND o.outcome_at >= now() - interval '730 days'
    ORDER BY o.outcome_at DESC
    LIMIT 200
  ) r
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS analytics_orders_uncharged_day ON analytics_orders (day, store_id)
  WHERE carrier_charge_minor IS NULL AND state IN ('delivered', 'returned');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS analytics_orders_uncharged_day;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.risk_signals(p_keys bytea[])
  RETURNS TABLE (phone_key bytea, outcome text, outcome_at timestamptz, store_no int)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT k.key, r.outcome, r.outcome_at, dense_rank() OVER (PARTITION BY k.key ORDER BY r.store_id)::int
  FROM (SELECT DISTINCT key FROM unnest(p_keys[1:100]) AS u(key)) k
  CROSS JOIN LATERAL (
    SELECT o.store_id, o.outcome, o.outcome_at FROM public.risk_orders o
    WHERE o.phone_key = k.key AND o.outcome IS NOT NULL AND o.outcome_at >= now() - interval '730 days'
    ORDER BY o.outcome_at DESC
    LIMIT 200
  ) r
$$;
-- +goose StatementEnd
-- A validated CHECK stays validated: nothing to undo.
