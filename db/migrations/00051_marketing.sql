-- Ad pixels (ADR-0043): a store's connection to each ad or analytics
-- platform it measures with, and when its pages may load them. One row
-- per provider; the Conversions API token is sealed (platform/envelope)
-- and never read back to the dashboard.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE IF NOT EXISTS store_pixels (
  store_id     uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  provider     text        NOT NULL CHECK (provider IN ('meta', 'tiktok', 'snap', 'google_ads', 'ga4')),
  -- id names the sealed token (its associated data), so a blob cannot be
  -- moved to another row.
  id           uuid        NOT NULL,
  pixel_id     text        NOT NULL CHECK (pixel_id ~ '^[A-Za-z0-9-]{4,64}$'),
  label        text        NOT NULL DEFAULT '' CHECK (label ~ '^[A-Za-z0-9_-]{0,40}$'),
  test_code    text        NOT NULL DEFAULT '' CHECK (test_code ~ '^[A-Za-z0-9_-]{0,40}$'),
  enabled      boolean     NOT NULL DEFAULT true,
  server_token bytea,
  version      int         NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id (the FK).
  PRIMARY KEY (store_id, provider)
);

CREATE TABLE IF NOT EXISTS marketing_settings (
  store_id   uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  consent    text        NOT NULL DEFAULT 'auto' CHECK (consent IN ('auto', 'required', 'off')),
  version    int         NOT NULL DEFAULT 1 CHECK (version >= 1),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE store_pixels ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_pixels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON store_pixels;
CREATE POLICY tenant_isolation ON store_pixels
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE marketing_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE marketing_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON marketing_settings;
CREATE POLICY tenant_isolation ON marketing_settings
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS marketing_settings;
DROP TABLE IF EXISTS store_pixels;
