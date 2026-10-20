-- Store assets (ADR-0016): images the storefront uses for its look,
-- starting with the logo. They follow the product image pipeline
-- (ADR-0012): presigned upload, re-encoded master, signed delivery.

-- +goose Up
CREATE TABLE store_assets (
  id             uuid        PRIMARY KEY,
  store_id       uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  kind           text        NOT NULL CHECK (kind IN ('logo')),
  status         text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'ready', 'failed')),
  upload_key     text        NOT NULL,
  object_key     text,
  format         text        CHECK (format IN ('jpeg', 'png')),
  width          integer     CHECK (width > 0),
  height         integer     CHECK (height > 0),
  bytes          bigint      CHECK (bytes > 0),
  failure_reason text        NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT store_assets_ready_has_master CHECK (status <> 'ready' OR (object_key IS NOT NULL AND width IS NOT NULL AND height IS NOT NULL))
);
-- Leads with store_id and covers the FK; finds a kind's newest asset.
CREATE INDEX store_assets_store_kind ON store_assets (store_id, kind, created_at DESC);
-- At most one live asset of each kind per store.
CREATE UNIQUE INDEX store_assets_one_ready ON store_assets (store_id, kind) WHERE status = 'ready';

ALTER TABLE store_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_assets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_assets
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE store_assets;
