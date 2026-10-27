-- Size charts (ADR-0035): a store keeps reusable size charts (centimetres,
-- free column names) and a product points at one. The product page shows
-- it under «جدول المقاسات».
--
-- header: ["المقاس", "الصدر", "الطول"]; body: [["S", "96", "68"], ...],
-- each row as long as the header (domain rules bound both).
--
-- Expand only: a new table, a nullable column, a foreign key added NOT
-- VALID and then validated (the column is all NULL, so the scan finds
-- nothing), and a partial index built without blocking writes. Deleting a
-- chart clears it from its products (SET NULL on that column alone).

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS size_charts (
  id         uuid        PRIMARY KEY,
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
  header     jsonb       NOT NULL CHECK (jsonb_typeof(header) = 'array'),
  body       jsonb       NOT NULL CHECK (jsonb_typeof(body) = 'array'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT size_charts_store_id_key UNIQUE (store_id, id)
);
-- The dashboard list, by name.
CREATE INDEX IF NOT EXISTS size_charts_store_name ON size_charts (store_id, name);

ALTER TABLE size_charts ENABLE ROW LEVEL SECURITY;
ALTER TABLE size_charts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON size_charts;
CREATE POLICY tenant_isolation ON size_charts
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

ALTER TABLE products ADD COLUMN IF NOT EXISTS size_chart_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'products_size_chart_fkey') THEN
    ALTER TABLE products ADD CONSTRAINT products_size_chart_fkey FOREIGN KEY (store_id, size_chart_id)
      REFERENCES size_charts (store_id, id) ON DELETE SET NULL (size_chart_id) NOT VALID;
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE products VALIDATE CONSTRAINT products_size_chart_fkey;
-- Covers the foreign key (a chart's delete) and "products using a chart".
CREATE INDEX CONCURRENTLY IF NOT EXISTS products_size_chart ON products (store_id, size_chart_id) WHERE size_chart_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS products_size_chart;
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_size_chart_fkey;
ALTER TABLE products DROP COLUMN IF EXISTS size_chart_id;
DROP TABLE IF EXISTS size_charts;
