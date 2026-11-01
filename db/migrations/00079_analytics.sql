-- The analytics module (ADR-0069): the profit dashboard's own facts,
-- estimates, ad spend and daily figures. Every table is tenant-owned
-- under RLS; the module reads other modules only through their api.go.
--
-- analytics_orders / analytics_lines: each order's facts, kept from the
--   orders module's events (and a backfill), with what the carrier kept
--   once its payout is known (shipping.Charges).
-- profit_settings: the store's estimates of delivery costs until then.
-- ad_spend: what the store spent on ads, typed in (or imported later).
-- analytics_dirty: the days whose figures must be computed again.
-- profit_daily: one row per day, dimension and key, read by the
--   dashboard on the read pool (ADR-0064).
-- analytics_backfills: the stores whose past was queued for learning.
--
-- app.analytics_dirty_stores and app.analytics_sweep_stores list stores
-- for the scheduled jobs, as their owner (the pattern of
-- app.billable_stores): store IDs only.

-- +goose Up
CREATE TABLE analytics_orders (
  store_id             uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id             uuid        NOT NULL,
  day                  date        NOT NULL,
  state                text        NOT NULL CHECK (state IN ('open', 'delivered', 'returned', 'cancelled')),
  governorate          text        NOT NULL CHECK (char_length(governorate) <= 10),
  campaign             text        NOT NULL DEFAULT '' CHECK (char_length(campaign) <= 100),
  currency             text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  revenue_minor        bigint      NOT NULL,
  shipping_minor       bigint      NOT NULL,
  cod_fee_minor        bigint      NOT NULL,
  on_delivery_minor    bigint      NOT NULL,
  deposit_paid_minor   bigint      NOT NULL DEFAULT 0,
  carrier_charge_minor bigint,
  risk_band            text        CHECK (risk_band IN ('new', 'low', 'medium', 'high')),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, order_id)
);
CREATE INDEX analytics_orders_store_day ON analytics_orders (store_id, day);
-- The carrier-charge sweep: settled orders not charged yet.
CREATE INDEX analytics_orders_uncharged ON analytics_orders (store_id, day)
  WHERE carrier_charge_minor IS NULL AND state IN ('delivered', 'returned');

CREATE TABLE analytics_lines (
  store_id      uuid   NOT NULL,
  order_id      uuid   NOT NULL,
  line          int    NOT NULL CHECK (line >= 0),
  product_id    uuid   NOT NULL,
  title         text   NOT NULL CHECK (char_length(title) <= 200),
  qty           int    NOT NULL CHECK (qty > 0),
  revenue_minor bigint NOT NULL,
  cost_minor    bigint CHECK (cost_minor >= 0),
  PRIMARY KEY (store_id, order_id, line),
  FOREIGN KEY (store_id, order_id) REFERENCES analytics_orders (store_id, order_id) ON DELETE CASCADE
);

CREATE TABLE profit_settings (
  store_id            uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  delivery_cost_minor bigint      CHECK (delivery_cost_minor >= 0),
  return_cost_minor   bigint      CHECK (return_cost_minor >= 0),
  cod_fee_bp          int         NOT NULL DEFAULT 0 CHECK (cod_fee_bp BETWEEN 0 AND 10000),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ad_spend (
  id           uuid        PRIMARY KEY,
  store_id     uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  day          date        NOT NULL,
  platform     text        NOT NULL CHECK (platform IN ('meta', 'tiktok', 'google', 'snap', 'other')),
  campaign     text        NOT NULL DEFAULT '' CHECK (char_length(campaign) <= 100),
  currency     text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  amount_minor bigint      NOT NULL CHECK (amount_minor BETWEEN 0 AND 1000000000000),
  source       text        NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'import')),
  note         text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ad_spend_store_day ON ad_spend (store_id, day DESC, id);

CREATE TABLE analytics_dirty (
  store_id uuid NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  day      date NOT NULL,
  PRIMARY KEY (store_id, day)
);

CREATE TABLE profit_daily (
  store_id            uuid    NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  dim                 text    NOT NULL CHECK (dim IN ('total', 'product', 'campaign', 'governorate')),
  day                 date    NOT NULL,
  key                 text    NOT NULL CHECK (char_length(key) <= 100),
  label               text    NOT NULL DEFAULT '' CHECK (char_length(label) <= 200),
  currency            text    NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  orders              int     NOT NULL,
  delivered           int     NOT NULL,
  returned            int     NOT NULL,
  cancelled           int     NOT NULL,
  open                int     NOT NULL,
  cost_missing        int     NOT NULL,
  revenue_minor       bigint  NOT NULL,
  delivery_paid_minor bigint  NOT NULL,
  product_cost_minor  bigint  NOT NULL,
  shipping_cost_minor bigint  NOT NULL,
  cod_fees_minor      bigint  NOT NULL,
  return_cost_minor   bigint  NOT NULL,
  ad_spend_minor      bigint  NOT NULL,
  PRIMARY KEY (store_id, dim, day, key)
);

CREATE TABLE analytics_backfills (
  store_id  uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  queued_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['analytics_orders', 'analytics_lines', 'profit_settings', 'ad_spend', 'analytics_dirty',
                           'profit_daily', 'analytics_backfills'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
REVOKE TRUNCATE ON analytics_orders, analytics_lines, profit_settings, ad_spend, analytics_dirty, profit_daily, analytics_backfills FROM app_user;
REVOKE UPDATE, DELETE ON analytics_backfills FROM app_user;

-- The scheduled jobs' store lists, as the tables' owner.
CREATE POLICY analytics_lookup ON analytics_dirty FOR SELECT TO app_owner USING (true);
CREATE POLICY analytics_lookup ON analytics_orders FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.analytics_dirty_stores()
  RETURNS SETOF uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT DISTINCT store_id FROM public.analytics_dirty
$$;
-- Stores with delivered or returned orders of the last 60 days whose
-- carrier charge is not known yet.
CREATE FUNCTION app.analytics_sweep_stores()
  RETURNS SETOF uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT DISTINCT store_id FROM public.analytics_orders
  WHERE carrier_charge_minor IS NULL AND state IN ('delivered', 'returned') AND day >= (now() - interval '60 days')::date
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.analytics_dirty_stores() FROM PUBLIC;
REVOKE ALL ON FUNCTION app.analytics_sweep_stores() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.analytics_dirty_stores() TO app_user;
GRANT EXECUTE ON FUNCTION app.analytics_sweep_stores() TO app_user;

-- The dashboard is a plan feature, like the risk rules (every paid plan).
INSERT INTO features (key, kind, name_ar, name_en, sort) VALUES
  ('profit', 'flag', 'لوحة الربح الصافي', 'Net profit dashboard', 120)
ON CONFLICT (key) DO NOTHING;
INSERT INTO plan_entitlements (plan_id, feature, value)
SELECT p.id, 'profit', CASE WHEN p.kind = 'free' THEN 0 ELSE 1 END FROM plans p
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM plan_entitlements WHERE feature = 'profit';
DELETE FROM features WHERE key = 'profit';
DROP FUNCTION IF EXISTS app.analytics_sweep_stores();
DROP FUNCTION IF EXISTS app.analytics_dirty_stores();
DROP TABLE IF EXISTS analytics_backfills;
DROP TABLE IF EXISTS profit_daily;
DROP TABLE IF EXISTS analytics_dirty;
DROP TABLE IF EXISTS ad_spend;
DROP TABLE IF EXISTS profit_settings;
DROP TABLE IF EXISTS analytics_lines;
DROP TABLE IF EXISTS analytics_orders;
