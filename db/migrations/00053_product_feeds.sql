-- Product feeds (ADR-0044): the key in a store's feed URLs. Without a row
-- the store has no feeds. The feeds list what its pages already show, so
-- the key only keeps strangers from fetching them; it is kept as is (the
-- dashboard shows the URLs) and the merchant can replace it.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE IF NOT EXISTS product_feeds (
  store_id   uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  key        text        NOT NULL CHECK (key ~ '^[a-z2-7]{32}$'),
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE product_feeds ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_feeds FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON product_feeds;
CREATE POLICY tenant_isolation ON product_feeds
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS product_feeds;
