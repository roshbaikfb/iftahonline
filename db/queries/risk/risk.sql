-- The risk module's queries (ADR-0065). Every one is on the store in
-- context (RLS), except RiskSignals and PurgeRiskOrders, which go through
-- the SECURITY DEFINER functions of migration 00073.

-- name: UpsertRiskOrders :exec
-- One row per order, in batches of parallel arrays. An empty outcome is
-- none yet. A row changes only when its outcome does, so a redelivered
-- event writes nothing.
INSERT INTO risk_orders (store_id, order_id, phone_key, placed_at, outcome, outcome_at)
SELECT @store_id, (@order_ids::uuid[])[n], (@phone_keys::bytea[])[n], (@placed_ats::timestamptz[])[n],
       nullif((@outcomes::text[])[n], ''),
       CASE WHEN (@outcomes::text[])[n] = '' THEN NULL ELSE (@outcome_ats::timestamptz[])[n] END
FROM generate_subscripts(@order_ids::uuid[], 1) AS n
ON CONFLICT (store_id, order_id) DO UPDATE
SET phone_key = excluded.phone_key, outcome = excluded.outcome, outcome_at = excluded.outcome_at, updated_at = now()
WHERE (risk_orders.outcome, risk_orders.outcome_at, risk_orders.phone_key)
      IS DISTINCT FROM (excluded.outcome, excluded.outcome_at, excluded.phone_key);

-- name: RiskOrderKeys :many
-- The phone keys of some of the store's orders; orders not learnt yet are
-- missing.
SELECT r.order_id, r.phone_key
FROM unnest(@order_ids::uuid[]) AS ids(id)
JOIN risk_orders r ON r.store_id = @store_id AND r.order_id = ids.id;

-- name: RiskHistory :one
-- A phone's orders in the store, by outcome.
SELECT count(*)::int AS orders,
       count(*) FILTER (WHERE outcome = 'delivered')::int AS delivered,
       count(*) FILTER (WHERE outcome = 'returned')::int AS returned,
       count(*) FILTER (WHERE outcome = 'refused')::int AS refused,
       count(*) FILTER (WHERE outcome = 'no_answer')::int AS no_answer,
       count(*) FILTER (WHERE outcome = 'unconfirmed')::int AS unconfirmed,
       coalesce(max(outcome_at), 'epoch')::timestamptz AS last_at -- epoch: no outcome yet
FROM risk_orders
WHERE store_id = @store_id AND phone_key = @phone_key;

-- name: RiskSignals :many
SELECT s.phone_key::bytea AS phone_key, s.outcome::text AS outcome, s.outcome_at::timestamptz AS outcome_at,
       s.store_no::int AS store_no
FROM app.risk_signals(@phone_keys::bytea[]) AS s;

-- name: QueueRiskBackfill :one
-- Marks the store's past as queued; no row when it already was.
INSERT INTO risk_backfills (store_id) VALUES (@store_id)
ON CONFLICT (store_id) DO NOTHING
RETURNING store_id;

-- name: PurgeRiskOrders :one
SELECT app.risk_purge(@max_rows::int)::int AS purged;

-- name: RiskSettings :many
-- The store's rules: no row is both off.
SELECT hold_from, verify_from, prepay_from FROM risk_settings WHERE store_id = @store_id;

-- name: SaveRiskSettings :exec
INSERT INTO risk_settings (store_id, hold_from, verify_from, prepay_from) VALUES (@store_id, @hold_from, @verify_from, @prepay_from)
ON CONFLICT (store_id) DO UPDATE
SET hold_from = excluded.hold_from, verify_from = excluded.verify_from, prepay_from = excluded.prepay_from, updated_at = now();
