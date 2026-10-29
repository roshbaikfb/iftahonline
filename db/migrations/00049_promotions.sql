-- Coupons (ADR-0042): a store's discount codes, and which orders hold
-- them. An order takes one coupon, in the transaction that places it:
-- the coupon's row is locked, its limits checked and its use counted,
-- so the last use is taken once. An order cancelled before it ships
-- gives its use back.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE IF NOT EXISTS coupons (
  store_id           uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  id                 uuid        NOT NULL,
  code               text        NOT NULL CHECK (code ~ '^[A-Z0-9_-]{3,32}$'),
  kind               text        NOT NULL CHECK (kind IN ('percent', 'fixed', 'free_shipping')),
  percent            int         NOT NULL DEFAULT 0 CHECK (percent BETWEEN 0 AND 100),
  amount_minor       bigint      NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
  min_subtotal_minor bigint      NOT NULL DEFAULT 0 CHECK (min_subtotal_minor >= 0),
  max_uses           int         CHECK (max_uses >= 1),
  max_per_customer   int         CHECK (max_per_customer >= 1),
  starts_at          timestamptz,
  ends_at            timestamptz,
  active             boolean     NOT NULL DEFAULT true,
  uses               int         NOT NULL DEFAULT 0 CHECK (uses >= 0),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, id),
  -- A shopper's code finds its coupon; leads with store_id (the FK).
  CONSTRAINT coupons_store_code_key UNIQUE (store_id, code)
);

-- One per order: an order takes one coupon.
CREATE TABLE IF NOT EXISTS coupon_redemptions (
  store_id       uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id       uuid        NOT NULL,
  coupon_id      uuid        NOT NULL,
  customer_id    uuid        NOT NULL,
  discount_minor bigint      NOT NULL CHECK (discount_minor >= 0),
  free_shipping  boolean     NOT NULL DEFAULT false,
  released_at    timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, order_id),
  FOREIGN KEY (store_id, coupon_id) REFERENCES coupons (store_id, id) ON DELETE RESTRICT
);
-- A customer's uses of a coupon (the per-customer limit), and the FK.
CREATE INDEX IF NOT EXISTS coupon_redemptions_customer ON coupon_redemptions (store_id, coupon_id, customer_id);

ALTER TABLE coupons ENABLE ROW LEVEL SECURITY;
ALTER TABLE coupons FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON coupons;
CREATE POLICY tenant_isolation ON coupons
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE coupon_redemptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE coupon_redemptions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON coupon_redemptions;
CREATE POLICY tenant_isolation ON coupon_redemptions
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON coupon_redemptions FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS coupon_redemptions;
DROP TABLE IF EXISTS coupons;
