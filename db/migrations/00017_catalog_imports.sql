-- Catalog imports (ADR-0014): one row per run of a CSV or WooCommerce
-- import, with progress the dashboard polls. cursor is the
-- number of source records already handled, so a retried job resumes
-- instead of starting over. WooCommerce credentials are sealed with the
-- platform keyring (secretbox), bound to the import ID.

-- +goose Up
CREATE TABLE catalog_imports (
  id              uuid        PRIMARY KEY,
  store_id        uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  kind            text        NOT NULL CHECK (kind IN ('csv', 'woocommerce')),
  status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'queued', 'running', 'succeeded', 'failed')),
  source_key      text,
  woo_url         text,
  woo_credentials bytea,
  total           integer     NOT NULL DEFAULT 0 CHECK (total >= 0),
  cursor          integer     NOT NULL DEFAULT 0 CHECK (cursor >= 0),
  created_count   integer     NOT NULL DEFAULT 0 CHECK (created_count >= 0),
  updated_count   integer     NOT NULL DEFAULT 0 CHECK (updated_count >= 0),
  failed_count    integer     NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
  -- [{"line": 12, "message": "..."}], at most 100 entries.
  row_errors      jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(row_errors) = 'array'),
  failure_reason  text        NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  started_at      timestamptz,
  finished_at     timestamptz,
  CONSTRAINT catalog_imports_source CHECK (
    (kind = 'woocommerce' AND woo_url IS NOT NULL AND woo_credentials IS NOT NULL) OR
    (kind <> 'woocommerce' AND source_key IS NOT NULL))
);
-- Leads with store_id and covers the FK; the dashboard lists newest first.
CREATE INDEX catalog_imports_store_list ON catalog_imports (store_id, id DESC);

ALTER TABLE catalog_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_imports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON catalog_imports
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE catalog_imports;
