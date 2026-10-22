-- Customers of a store (ADR-0020). A customer is a phone number in one
-- store: the natural key of cash-on-delivery commerce, where shoppers
-- order as guests and the merchant calls to confirm. The same person in
-- two stores is two unrelated customers.
--
-- The phone is kept in full (the merchant calls it and couriers need it)
-- in canonical E.164; it is never logged and never put in audit
-- metadata. name_key is the name in search form (arabic.NormalizeSearch)
-- so "احمد" finds "أحمد".

-- +goose Up
CREATE TABLE customers (
  id            uuid        PRIMARY KEY,
  store_id      uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  phone         text        NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
  name          text        NOT NULL DEFAULT '' CHECK (char_length(name) <= 120),
  name_key      text        NOT NULL DEFAULT '' CHECK (char_length(name_key) <= 480),
  orders_count  integer     NOT NULL DEFAULT 0 CHECK (orders_count >= 0),
  last_order_at timestamptz,
  blocked_at    timestamptz,
  block_reason  text        NOT NULL DEFAULT '' CHECK (char_length(block_reason) <= 300),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT customers_store_id_key UNIQUE (store_id, id),
  -- The natural key; leads with store_id, so it also covers the FK.
  CONSTRAINT customers_store_phone_key UNIQUE (store_id, phone)
);
-- The dashboard list, newest first, and the block list.
CREATE INDEX customers_store_list ON customers (store_id, id DESC);
CREATE INDEX customers_store_blocked ON customers (store_id, id DESC) WHERE blocked_at IS NOT NULL;

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE customers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customers
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE customers;
