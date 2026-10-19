-- Catalog: products, variants, stock and categories (ADR-0011).
--
-- Every table is tenant-owned (store_id + RLS, ADR-0002). Children carry
-- composite foreign keys (store_id, parent_id) to their parent, so a row
-- can never point at another store's product even if application code
-- passed the wrong ID: PostgreSQL rejects the insert.
--
-- Products and variants are soft-deleted (deleted_at). Slugs and SKUs are
-- unique per store among live rows only, so deleting frees them.
--
-- Stock: inventory_levels.reserved counts units held by active
-- stock_reservations. Reservations change it with one conditional UPDATE
-- (on_hand - reserved >= qty), so concurrent checkouts can never oversell;
-- the CHECK is the last wall.

-- +goose Up
CREATE TABLE products (
  id          uuid        PRIMARY KEY,
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  slug        citext      NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 120),
  title       text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  description text        NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
  status      text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
  -- [{"name": "Size", "values": ["S", "M"]}, ...], at most 3 (domain rules).
  options     jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(options) = 'array'),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  deleted_at  timestamptz,
  CONSTRAINT products_store_id_key UNIQUE (store_id, id)
);
CREATE UNIQUE INDEX products_store_slug_key ON products (store_id, slug) WHERE deleted_at IS NULL;
-- The dashboard list: newest first (ULIDs sort by creation time).
CREATE INDEX products_store_list ON products (store_id, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX products_store_status_list ON products (store_id, status, id DESC) WHERE deleted_at IS NULL;

CREATE TABLE variants (
  id               uuid        PRIMARY KEY,
  store_id         uuid        NOT NULL,
  product_id       uuid        NOT NULL,
  sku              citext      CHECK (char_length(sku) BETWEEN 1 AND 64),
  option_values    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(option_values) <= 3),
  price_minor      bigint      NOT NULL CHECK (price_minor >= 0),
  compare_at_minor bigint      CHECK (compare_at_minor > price_minor),
  currency         char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  position         integer     NOT NULL CHECK (position >= 0),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  deleted_at       timestamptz,
  CONSTRAINT variants_store_id_key UNIQUE (store_id, id),
  CONSTRAINT variants_product_fkey FOREIGN KEY (store_id, product_id)
    REFERENCES products (store_id, id) ON DELETE CASCADE
);
-- Leads with store_id and covers the (store_id, product_id) FK.
CREATE INDEX variants_store_product ON variants (store_id, product_id, position);
CREATE UNIQUE INDEX variants_store_sku_key ON variants (store_id, sku) WHERE sku IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX variants_product_options_key ON variants (product_id, option_values) WHERE deleted_at IS NULL;

-- One row per variant (a single location for now). Kept apart from
-- variants so hot stock updates do not rewrite the wider variant row.
CREATE TABLE inventory_levels (
  variant_id uuid        PRIMARY KEY,
  store_id   uuid        NOT NULL,
  tracked    boolean     NOT NULL DEFAULT true,
  on_hand    integer     NOT NULL DEFAULT 0,
  reserved   integer     NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT inventory_levels_bounds CHECK (on_hand >= 0 AND reserved >= 0 AND reserved <= on_hand),
  CONSTRAINT inventory_levels_variant_fkey FOREIGN KEY (store_id, variant_id)
    REFERENCES variants (store_id, id) ON DELETE CASCADE
);
CREATE INDEX inventory_levels_store_variant ON inventory_levels (store_id, variant_id);

-- One row per (ref, variant). ref is the caller's key (a checkout or
-- order ID), which makes ReserveStock idempotent. counted records whether
-- the line incremented inventory_levels.reserved (tracked stock), so a
-- release undoes exactly what the reservation did.
CREATE TABLE stock_reservations (
  id          uuid        PRIMARY KEY,
  store_id    uuid        NOT NULL,
  ref         text        NOT NULL CHECK (char_length(ref) BETWEEN 1 AND 100),
  variant_id  uuid        NOT NULL,
  qty         integer     NOT NULL CHECK (qty > 0),
  counted     boolean     NOT NULL,
  status      text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'expired')),
  expires_at  timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  released_at timestamptz,
  CONSTRAINT stock_reservations_ref_variant_key UNIQUE (store_id, ref, variant_id),
  CONSTRAINT stock_reservations_variant_fkey FOREIGN KEY (store_id, variant_id)
    REFERENCES variants (store_id, id) ON DELETE CASCADE
);
CREATE INDEX stock_reservations_variant_fk ON stock_reservations (store_id, variant_id);

CREATE TABLE categories (
  id         uuid        PRIMARY KEY,
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  parent_id  uuid,
  slug       citext      NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 120),
  name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  position   integer     NOT NULL DEFAULT 0 CHECK (position >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT categories_store_id_key UNIQUE (store_id, id),
  CONSTRAINT categories_store_slug_key UNIQUE (store_id, slug),
  CONSTRAINT categories_not_own_parent CHECK (parent_id IS DISTINCT FROM id),
  -- A parent with children cannot be deleted (the app reports 409).
  CONSTRAINT categories_parent_fkey FOREIGN KEY (store_id, parent_id)
    REFERENCES categories (store_id, id) ON DELETE RESTRICT
);
CREATE INDEX categories_store_parent ON categories (store_id, parent_id);

CREATE TABLE product_categories (
  store_id    uuid NOT NULL,
  product_id  uuid NOT NULL,
  category_id uuid NOT NULL,
  PRIMARY KEY (store_id, product_id, category_id),
  CONSTRAINT product_categories_product_fkey FOREIGN KEY (store_id, product_id)
    REFERENCES products (store_id, id) ON DELETE CASCADE,
  CONSTRAINT product_categories_category_fkey FOREIGN KEY (store_id, category_id)
    REFERENCES categories (store_id, id) ON DELETE CASCADE
);
-- The primary key covers the product FK; this one covers the category FK
-- and the "products in category" filter.
CREATE INDEX product_categories_category ON product_categories (store_id, category_id, product_id);

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON products
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE variants ENABLE ROW LEVEL SECURITY;
ALTER TABLE variants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON variants
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE inventory_levels ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_levels FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inventory_levels
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE stock_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_reservations
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE categories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON categories
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE product_categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_categories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_categories
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE product_categories;
DROP TABLE categories;
DROP TABLE stock_reservations;
DROP TABLE inventory_levels;
DROP TABLE variants;
DROP TABLE products;
