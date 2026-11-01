-- The risk module (ADR-0065): what each order taught about its
-- customer's phone, and the one platform-wide read of it.
--
-- risk_orders has one row per order, written by the risk module from
-- the orders module's events: the phone's key, when the order was placed,
-- and its outcome once it has one. The key is HMAC-SHA256(RISK_PHONE_KEY,
-- E.164), so the table holds no phone number, and the same customer has
-- the same key in every store. Rows stay tenant-owned under RLS: a store
-- reads and writes only its own.
--
-- app.risk_signals is the only read across stores. It returns, for each
-- of at most 100 phone keys, its most recent outcomes (at most 200, none
-- older than the score's 730-day horizon) with an opaque store number that is the same for
-- one store's outcomes of that phone. Never a store or an order ID, never
-- a count of orders without an outcome. The api passes it only the keys
-- of the calling store's own orders (ADR-0065's privacy notes).
--
-- Not partitioned: partitions are only made from the current month on,
-- and the backfill writes rows for orders placed long ago. Rows placed
-- more than 760 days ago are purged by app.risk_purge (daily job): the
-- score ignores outcomes older than 730 days.
--
-- risk_backfills marks the stores whose past orders were queued for
-- learning, so each store is backfilled once.

-- +goose Up
CREATE TABLE risk_orders (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id   uuid        NOT NULL,
  phone_key  bytea       NOT NULL CHECK (octet_length(phone_key) = 32),
  placed_at  timestamptz NOT NULL,
  outcome    text        CHECK (outcome IN ('delivered', 'returned', 'refused', 'no_answer', 'unconfirmed')),
  outcome_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, order_id),
  CONSTRAINT risk_orders_outcome_at CHECK ((outcome IS NULL) = (outcome_at IS NULL))
);
-- The platform-wide lookup: a phone's latest outcomes.
CREATE INDEX risk_orders_phone_outcome ON risk_orders (phone_key, outcome_at DESC) WHERE outcome IS NOT NULL;
-- A store's own history of a phone.
CREATE INDEX risk_orders_store_phone ON risk_orders (store_id, phone_key);
-- The purge.
CREATE INDEX risk_orders_placed_at ON risk_orders (placed_at);

ALTER TABLE risk_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON risk_orders
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
-- The table's owner reads every store's rows only inside the functions
-- below (FORCE RLS binds it too; app_user gets the functions, not this).
CREATE POLICY risk_lookup ON risk_orders FOR SELECT TO app_owner USING (true);
CREATE POLICY risk_purge ON risk_orders FOR DELETE TO app_owner USING (true);

CREATE TABLE risk_backfills (
  store_id  uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  queued_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE risk_backfills ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_backfills FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON risk_backfills
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON risk_backfills FROM app_user;
REVOKE DELETE, TRUNCATE ON risk_orders FROM app_user;

-- +goose StatementBegin
CREATE FUNCTION app.risk_signals(p_keys bytea[])
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
REVOKE ALL ON FUNCTION app.risk_signals(bytea[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.risk_signals(bytea[]) TO app_user;

-- Deletes up to p_limit rows placed more than 760 days ago (the score's
-- 730-day horizon, and a month to spare); returns how many. The cut-off
-- is the function's own, so a caller cannot purge younger rows.
-- +goose StatementBegin
CREATE FUNCTION app.risk_purge(p_limit int)
  RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
DECLARE n int;
BEGIN
  DELETE FROM public.risk_orders WHERE ctid IN (
    SELECT ctid FROM public.risk_orders WHERE placed_at < now() - interval '760 days' LIMIT least(greatest(p_limit, 0), 10000));
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.risk_purge(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.risk_purge(int) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.risk_purge(int);
DROP FUNCTION IF EXISTS app.risk_signals(bytea[]);
DROP TABLE IF EXISTS risk_backfills;
DROP TABLE IF EXISTS risk_orders;
