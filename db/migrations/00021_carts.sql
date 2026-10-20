-- Shopping carts (ADR-0019). A cart belongs to one store and is found by
-- the hash of a random token kept in the shopper's cookie: the token is
-- the only thing that grants access, so it is never stored in clear.
-- Lines name catalog variants by ID only (no foreign key across
-- modules); prices and stock are read live from the catalog.

-- +goose Up
CREATE TABLE carts (
  id         uuid        PRIMARY KEY,
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  token_hash bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT carts_store_id_key UNIQUE (store_id, id),
  -- Leads with store_id: covers the FK and the lookup by token.
  CONSTRAINT carts_store_token_key UNIQUE (store_id, token_hash)
);
CREATE INDEX carts_store_expires ON carts (store_id, expires_at);

ALTER TABLE carts ENABLE ROW LEVEL SECURITY;
ALTER TABLE carts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON carts
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

CREATE TABLE cart_items (
  store_id   uuid        NOT NULL,
  cart_id    uuid        NOT NULL,
  variant_id uuid        NOT NULL,
  qty        integer     NOT NULL CHECK (qty BETWEEN 1 AND 20),
  added_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, cart_id, variant_id),
  CONSTRAINT cart_items_cart_fkey FOREIGN KEY (store_id, cart_id) REFERENCES carts (store_id, id) ON DELETE CASCADE
);

ALTER TABLE cart_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE cart_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cart_items
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE cart_items;
DROP TABLE carts;
