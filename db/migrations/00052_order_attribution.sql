-- Where orders come from (ADR-0043).
--
-- orders gains the UTM parameters of the visit that brought the shopper:
-- nullable columns, so old code (which writes none) keeps working; the
-- length checks are NOT VALID (migsafe), and every old row has NULLs.
--
-- order_signals holds what ad providers' server APIs match a conversion
-- with: their cookies and click IDs, the shopper's IP address and user
-- agent. One row per order, written in the transaction that places it.
-- Partitioned by month on the order's time and dropped after one whole
-- month: it is personal data kept only as long as the conversions need.
--
-- Expand only.

-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS utm_source text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS utm_medium text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS utm_campaign text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS utm_content text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS utm_term text;
ALTER TABLE orders ADD CONSTRAINT orders_utm_length CHECK (
  char_length(utm_source) <= 100 AND char_length(utm_medium) <= 100 AND char_length(utm_campaign) <= 100 AND
  char_length(utm_content) <= 100 AND char_length(utm_term) <= 100) NOT VALID;

CREATE TABLE IF NOT EXISTS order_signals (
  store_id         uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_created_at timestamptz NOT NULL,
  order_id         uuid        NOT NULL,
  fbp              text        NOT NULL DEFAULT '' CHECK (char_length(fbp) <= 255),
  fbc              text        NOT NULL DEFAULT '' CHECK (char_length(fbc) <= 255),
  ttp              text        NOT NULL DEFAULT '' CHECK (char_length(ttp) <= 255),
  ttclid           text        NOT NULL DEFAULT '' CHECK (char_length(ttclid) <= 255),
  sccid            text        NOT NULL DEFAULT '' CHECK (char_length(sccid) <= 255),
  gclid            text        NOT NULL DEFAULT '' CHECK (char_length(gclid) <= 255),
  ip               inet,
  user_agent       text        NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 512),
  -- Leads with store_id (the FK); an order's signals are read by its ID
  -- and time, so one partition.
  PRIMARY KEY (store_id, order_created_at, order_id)
) PARTITION BY RANGE (order_created_at);

ALTER TABLE order_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_signals FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON order_signals;
CREATE POLICY tenant_isolation ON order_signals
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON order_signals FROM app_user;

INSERT INTO app.monthly_partitioned (parent, key_column, append_only, retain_months) VALUES
  ('order_signals', 'order_created_at', true, 1)
ON CONFLICT DO NOTHING;
SELECT app.ensure_monthly_partitions(3, 'order_signals');

-- +goose Down
DELETE FROM app.monthly_partitioned WHERE parent = 'order_signals';
DROP TABLE IF EXISTS order_signals;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_utm_length;
ALTER TABLE orders DROP COLUMN IF EXISTS utm_term;
ALTER TABLE orders DROP COLUMN IF EXISTS utm_content;
ALTER TABLE orders DROP COLUMN IF EXISTS utm_campaign;
ALTER TABLE orders DROP COLUMN IF EXISTS utm_medium;
ALTER TABLE orders DROP COLUMN IF EXISTS utm_source;
