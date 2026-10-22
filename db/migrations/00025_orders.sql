-- Orders (ADR-0021). orders, order_items and order_events are partitioned
-- by month on the order's creation time, which the order's ULID encodes:
-- a lookup by ID computes created_at and reaches one partition. Items and
-- events share the order's partition key, so an order's whole history
-- lives in one month. The partitions are kept ahead by the platform job
-- (ADR-0020); this migration registers the three tables.
--
-- Lines name catalog variants and products by ID only (no foreign key
-- across modules) and copy titles and prices, so an order reads the same
-- after the catalog changes. customer_id names the customers module's
-- row the same way.
--
-- order_idempotency maps a client's Idempotency-Key to the order it
-- created. It is not partitioned (the key must be unique per store).
-- Keys are kept a week: each keyed order deletes a few of its own store's
-- expired keys in its transaction, so cleanup needs no cross-store job
-- (FORCE RLS binds the table owner too).

-- +goose Up
CREATE TABLE orders (
  id                uuid        NOT NULL,
  store_id          uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  created_at        timestamptz NOT NULL,
  status            text        NOT NULL DEFAULT 'pending' CHECK (status IN
                      ('pending', 'confirmed', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'failed_delivery', 'returned')),
  version           integer     NOT NULL DEFAULT 1 CHECK (version > 0),
  source            text        NOT NULL CHECK (source IN ('storefront', 'merchant')),
  customer_id       uuid        NOT NULL,
  name              text        NOT NULL CHECK (char_length(name) BETWEEN 2 AND 120),
  phone             text        NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
  governorate       text        NOT NULL CHECK (governorate ~ '^[A-Z]{2}-[A-Z0-9]{1,3}$'),
  city              text        NOT NULL CHECK (char_length(city) BETWEEN 2 AND 80),
  address           text        NOT NULL CHECK (char_length(address) BETWEEN 5 AND 300),
  notes             text        NOT NULL DEFAULT '' CHECK (char_length(notes) <= 500),
  currency          text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  subtotal_minor    bigint      NOT NULL CHECK (subtotal_minor >= 0),
  shipping_minor    bigint      NOT NULL DEFAULT 0 CHECK (shipping_minor >= 0),
  total_minor       bigint      NOT NULL,
  item_count        integer     NOT NULL CHECK (item_count > 0),
  -- What was ordered (variants and quantities), for the duplicate window.
  lines_hash        bytea       NOT NULL CHECK (octet_length(lines_hash) = 32),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  status_changed_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT orders_total CHECK (total_minor = subtotal_minor + shipping_minor),
  -- Leads with store_id (covers the FK); walked backwards for the
  -- newest-first list.
  PRIMARY KEY (store_id, created_at, id)
) PARTITION BY RANGE (created_at);
-- Each list filter has an index that leads its walk (ADR-0021).
CREATE INDEX orders_store_status ON orders (store_id, status, created_at DESC, id DESC);
CREATE INDEX orders_store_phone ON orders (store_id, phone, created_at DESC, id DESC);
CREATE INDEX orders_store_customer ON orders (store_id, customer_id, created_at DESC, id DESC);

CREATE TABLE order_items (
  store_id         uuid        NOT NULL,
  order_created_at timestamptz NOT NULL,
  order_id         uuid        NOT NULL,
  line             smallint    NOT NULL CHECK (line BETWEEN 1 AND 50),
  variant_id       uuid        NOT NULL,
  product_id       uuid        NOT NULL,
  title            text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  variant_title    text        NOT NULL DEFAULT '' CHECK (char_length(variant_title) <= 200),
  sku              text        NOT NULL DEFAULT '' CHECK (char_length(sku) <= 64),
  qty              integer     NOT NULL CHECK (qty BETWEEN 1 AND 100),
  unit_price_minor bigint      NOT NULL CHECK (unit_price_minor >= 0),
  total_minor      bigint      NOT NULL,
  CONSTRAINT order_items_total CHECK (total_minor = unit_price_minor * qty),
  PRIMARY KEY (store_id, order_created_at, order_id, line),
  CONSTRAINT order_items_order_fkey FOREIGN KEY (store_id, order_created_at, order_id)
    REFERENCES orders (store_id, created_at, id) ON DELETE CASCADE
) PARTITION BY RANGE (order_created_at);

-- The order's timeline: one row per transition, written in the
-- transition's transaction. Append-only.
CREATE TABLE order_events (
  id               uuid        NOT NULL,
  store_id         uuid        NOT NULL,
  order_created_at timestamptz NOT NULL,
  order_id         uuid        NOT NULL,
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  from_status      text,
  to_status        text        NOT NULL,
  actor_user_id    uuid,
  note             text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
  PRIMARY KEY (store_id, order_created_at, order_id, id),
  CONSTRAINT order_events_order_fkey FOREIGN KEY (store_id, order_created_at, order_id)
    REFERENCES orders (store_id, created_at, id) ON DELETE CASCADE
) PARTITION BY RANGE (order_created_at);

CREATE TABLE order_idempotency (
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  key         text        NOT NULL CHECK (char_length(key) BETWEEN 1 AND 100),
  fingerprint bytea       NOT NULL CHECK (octet_length(fingerprint) = 32),
  order_id    uuid        NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, key)
);
CREATE INDEX order_idempotency_store_created ON order_idempotency (store_id, created_at);

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_items
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE order_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_events
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON order_events FROM app_user;
ALTER TABLE order_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_idempotency FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_idempotency
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

INSERT INTO app.monthly_partitioned (parent, key_column, append_only) VALUES
  ('orders', 'created_at', false),
  ('order_items', 'order_created_at', false),
  ('order_events', 'order_created_at', true);
SELECT app.ensure_monthly_partitions(3, 'orders');
SELECT app.ensure_monthly_partitions(3, 'order_items');
SELECT app.ensure_monthly_partitions(3, 'order_events');

-- +goose Down
DELETE FROM app.monthly_partitioned WHERE parent IN ('orders', 'order_items', 'order_events');
DROP TABLE order_idempotency;
DROP TABLE order_events;
DROP TABLE order_items;
DROP TABLE orders;
