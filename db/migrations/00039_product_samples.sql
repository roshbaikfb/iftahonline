-- Sample products (ADR-0034): the setup wizard's starter template fills a
-- new store with products marked as samples. Shoppers see them, marked;
-- nobody can order them; the merchant deletes them in one go.
--
-- Expand only: a column with a constant default (no table rewrite), and
-- a partial index built without blocking writes, for "this store's
-- samples" (a handful of rows per store, or none).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE products ADD COLUMN is_sample boolean NOT NULL DEFAULT false;
CREATE INDEX CONCURRENTLY IF NOT EXISTS products_samples ON products (store_id) WHERE is_sample AND deleted_at IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS products_samples;
ALTER TABLE products DROP COLUMN is_sample;
