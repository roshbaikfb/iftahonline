-- Plans as data, and each store's subscription (ADR-0045).
--
-- Plans, their prices and what they grant are platform data: no store
-- owns them, so they have no store_id and no RLS. Merchant code may only
-- read them; the console changes them (P7 7e). A change to a plan is a
-- new version (plans.version); a subscription points at the version it
-- bought, so a price change never reaches a store before it renews.
--
-- What a plan grants is a row per feature: a flag (0 or 1), a count the
-- store may hold, or units per calendar month. NULL is unlimited; a
-- feature a plan does not list is not granted. The features table is the
-- catalog: the console may add keys of its own, which the code shows but
-- does not enforce until a module asks for them (ADR-0045).
--
-- subscriptions is a tenant table: one row per store. A store without a
-- row is on the free plan. Existing stores start a 14-day trial of `pro`
-- from this migration (approved 2026-10-30); then they fall back to free.
--
-- The seeded prices and limits are placeholders, to be set from the
-- console before launch (docs/go-live.md).
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE features (
  key        text        PRIMARY KEY CHECK (key ~ '^[a-z][a-z_]{0,31}$'),
  kind       text        NOT NULL CHECK (kind IN ('flag', 'count', 'monthly')),
  name_ar    text        NOT NULL CHECK (char_length(name_ar) BETWEEN 1 AND 80),
  name_en    text        NOT NULL CHECK (char_length(name_en) BETWEEN 1 AND 80),
  sort       int         NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plans (
  id         uuid        PRIMARY KEY,
  code       text        NOT NULL CHECK (code ~ '^[a-z][a-z_]{1,31}$'),
  version    int         NOT NULL DEFAULT 1 CHECK (version >= 1),
  kind       text        NOT NULL CHECK (kind IN ('free', 'pays', 'subscription', 'agency')),
  name_ar    text        NOT NULL CHECK (char_length(name_ar) BETWEEN 1 AND 80),
  name_en    text        NOT NULL CHECK (char_length(name_en) BETWEEN 1 AND 80),
  trial_days int         NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 90),
  public     boolean     NOT NULL DEFAULT true,
  status     text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
  sort       int         NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT plans_code_version_key UNIQUE (code, version)
);
-- One version of a plan is on sale at a time, and one plan is the free
-- one a store falls back to.
CREATE UNIQUE INDEX plans_active_code_key ON plans (code) WHERE status = 'active';
CREATE UNIQUE INDEX plans_active_free_key ON plans (kind) WHERE status = 'active' AND kind = 'free';

CREATE TABLE plan_prices (
  plan_id      uuid   NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
  currency     char(3) NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  cycle        text   NOT NULL CHECK (cycle IN ('monthly', 'annual')),
  amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
  -- Leads with plan_id, so it also covers the FK.
  PRIMARY KEY (plan_id, currency, cycle)
);

CREATE TABLE plan_entitlements (
  plan_id uuid   NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
  feature text   NOT NULL REFERENCES features (key),
  value   bigint CHECK (value IS NULL OR value >= 0), -- NULL: unlimited
  PRIMARY KEY (plan_id, feature)
);
CREATE INDEX plan_entitlements_feature_idx ON plan_entitlements (feature);

CREATE TABLE subscriptions (
  store_id             uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  plan_id              uuid        NOT NULL REFERENCES plans (id),
  status               text        NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'suspended', 'cancelled')),
  cycle                text        CHECK (cycle IN ('monthly', 'annual')),
  trial_ends_at        timestamptz,
  period_start         timestamptz,
  period_end           timestamptz,
  grace_until          timestamptz,
  suspended_at         timestamptz,
  cancel_at_period_end boolean     NOT NULL DEFAULT false,
  trial_used           boolean     NOT NULL DEFAULT false,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'trialing' OR trial_ends_at IS NOT NULL),
  CHECK (status <> 'past_due' OR grace_until IS NOT NULL),
  CHECK (status NOT IN ('suspended', 'cancelled') OR suspended_at IS NOT NULL),
  CHECK ((period_start IS NULL) = (period_end IS NULL) AND (period_end IS NULL OR period_start < period_end))
);
CREATE INDEX subscriptions_plan_id_idx ON subscriptions (plan_id);

INSERT INTO features (key, kind, name_ar, name_en, sort) VALUES
  ('orders_per_month', 'monthly', 'الطلبات في الشهر', 'Orders a month', 10),
  ('messages', 'monthly', 'رسائل واتساب وSMS في الشهر', 'WhatsApp and SMS messages a month', 20),
  ('products', 'count', 'المنتجات', 'Products', 30),
  ('staff', 'count', 'أعضاء الفريق', 'Team members', 40),
  ('custom_domains', 'count', 'الدومينات الخاصة', 'Custom domains', 50),
  ('card_payments', 'flag', 'الدفع بالبطاقة', 'Card payments', 60),
  ('coupons', 'flag', 'الكوبونات', 'Coupons', 70),
  ('pixels', 'flag', 'بكسلات الإعلانات', 'Ad pixels', 80),
  ('feeds', 'flag', 'فيدات المنتجات', 'Product feeds', 90),
  ('ad_free', 'flag', 'متجر بدون إعلانات', 'No ads on the store', 100);

INSERT INTO plans (id, code, kind, name_ar, name_en, trial_days, sort) VALUES
  ('01a18c4a-371b-0958-eb71-f1bd7a8f6803', 'free', 'free', 'مجانية', 'Free', 0, 10),
  ('01a18c4a-371c-feac-af27-f6984dc725d6', 'pays', 'pays', 'ادفع حسب مبيعاتك', 'Pay as you sell', 0, 20),
  ('01a18c4a-371d-1be3-397c-396c2bd8dac2', 'merchant', 'subscription', 'تاجر', 'Merchant', 14, 30),
  ('01a18c4a-371e-7246-8b89-e51a9e9d56da', 'pro', 'subscription', 'برو', 'Pro', 14, 40),
  ('01a18c4a-371f-4770-4be7-c3f713c934ac', 'brand', 'subscription', 'براند', 'Brand', 14, 50),
  ('01a18c4a-3720-107f-a97b-2bb69e291391', 'agency', 'agency', 'وكالة', 'Agency', 14, 60);

-- Annual is ten months: two months free.
INSERT INTO plan_prices (plan_id, currency, cycle, amount_minor)
SELECT p.id, v.currency, v.cycle, v.amount_minor
FROM (VALUES
  ('pays', 'EGP', 'monthly', 0), ('pays', 'SAR', 'monthly', 0), ('pays', 'AED', 'monthly', 0),
  ('merchant', 'EGP', 'monthly', 29900), ('merchant', 'EGP', 'annual', 299000),
  ('merchant', 'SAR', 'monthly', 4900), ('merchant', 'SAR', 'annual', 49000),
  ('merchant', 'AED', 'monthly', 4900), ('merchant', 'AED', 'annual', 49000),
  ('pro', 'EGP', 'monthly', 59900), ('pro', 'EGP', 'annual', 599000),
  ('pro', 'SAR', 'monthly', 9900), ('pro', 'SAR', 'annual', 99000),
  ('pro', 'AED', 'monthly', 9900), ('pro', 'AED', 'annual', 99000),
  ('brand', 'EGP', 'monthly', 149900), ('brand', 'EGP', 'annual', 1499000),
  ('brand', 'SAR', 'monthly', 24900), ('brand', 'SAR', 'annual', 249000),
  ('brand', 'AED', 'monthly', 24900), ('brand', 'AED', 'annual', 249000),
  ('agency', 'EGP', 'monthly', 399900), ('agency', 'EGP', 'annual', 3999000),
  ('agency', 'SAR', 'monthly', 69900), ('agency', 'SAR', 'annual', 699000),
  ('agency', 'AED', 'monthly', 69900), ('agency', 'AED', 'annual', 699000)
) AS v (code, currency, cycle, amount_minor)
JOIN plans p ON p.code = v.code;

-- Columns: free, pays, merchant, pro, brand, agency. NULL is unlimited.
INSERT INTO plan_entitlements (plan_id, feature, value)
SELECT p.id, v.feature, (v.vals)[p.sort / 10]
FROM (VALUES
  ('orders_per_month', ARRAY[30, NULL, 300, 1500, NULL, NULL]::bigint[]),
  ('messages', ARRAY[0, 300, 500, 2000, 10000, 10000]::bigint[]),
  ('products', ARRAY[50, 1000, 500, 5000, NULL, NULL]::bigint[]),
  ('staff', ARRAY[1, 2, 3, 10, 25, 50]::bigint[]),
  ('custom_domains', ARRAY[0, 1, 1, 5, 5, 5]::bigint[]),
  ('card_payments', ARRAY[0, 1, 1, 1, 1, 1]::bigint[]),
  ('coupons', ARRAY[0, 1, 1, 1, 1, 1]::bigint[]),
  ('pixels', ARRAY[0, 1, 1, 1, 1, 1]::bigint[]),
  ('feeds', ARRAY[0, 1, 0, 1, 1, 1]::bigint[]),
  ('ad_free', ARRAY[0, 1, 1, 1, 1, 1]::bigint[])
) AS v (feature, vals)
CROSS JOIN plans p;

-- Existing stores: a 14-day trial of pro. The table has no RLS yet, and
-- the migrating role reads stores through its directory_lookup policy.
INSERT INTO subscriptions (store_id, plan_id, status, trial_ends_at, trial_used)
SELECT s.id, p.id, 'trialing', now() + interval '14 days', true
FROM stores s JOIN plans p ON p.code = 'pro' AND p.status = 'active';

ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscriptions
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON subscriptions FROM app_user;

-- Platform data: merchant code reads it only.
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON features, plans, plan_prices, plan_entitlements FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plan_entitlements;
DROP TABLE IF EXISTS plan_prices;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS features;
