-- Bestsellers (ADR-0033): units sold per product and day, from
-- confirmed orders, for the storefront's "bestsellers" grids.
--
-- order_sales_counted records which orders are counted, so the counting
-- job (an event subscriber that may run twice or out of order) adds an
-- order's units once and takes them back once. A day is the order's
-- creation date (UTC), so a cancellation takes back from the day it
-- added to. Expand only: two new tables.

-- +goose Up
CREATE TABLE product_sales (
  store_id   uuid    NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  product_id uuid    NOT NULL,
  day        date    NOT NULL,
  units      integer NOT NULL CHECK (units >= 0),
  PRIMARY KEY (store_id, product_id, day)
);
-- The bestsellers read: a store's recent days.
CREATE INDEX product_sales_store_day ON product_sales (store_id, day);

CREATE TABLE order_sales_counted (
  store_id uuid NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id uuid NOT NULL,
  PRIMARY KEY (store_id, order_id)
);

ALTER TABLE product_sales ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_sales FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_sales
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE order_sales_counted ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_sales_counted FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_sales_counted
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE order_sales_counted;
DROP TABLE product_sales;
