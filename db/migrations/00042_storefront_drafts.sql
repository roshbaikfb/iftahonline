-- Drafts and previews (ADR-0036). The merchant edits the theme and the
-- home page as a draft, looks at the shop with it through a short-lived
-- preview link, then publishes. Shoppers only ever see what was
-- published (store_themes, store_pages).
--
-- storefront_drafts: one row per store, each part NULL when there is no
-- draft of it. theme is the domain's ThemeSettings; home is the home
-- page's title, description and sections.
-- storefront_previews: a preview link's token, stored as its SHA-256 so
-- the database never holds a usable link; it expires on its own.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE IF NOT EXISTS storefront_drafts (
  store_id   uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  theme      jsonb       CHECK (theme IS NULL OR jsonb_typeof(theme) = 'object'),
  home       jsonb       CHECK (home IS NULL OR jsonb_typeof(home) = 'object'),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE storefront_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_drafts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storefront_drafts;
CREATE POLICY tenant_isolation ON storefront_drafts
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

CREATE TABLE IF NOT EXISTS storefront_previews (
  token_hash bytea       PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
-- Covers the FK and the store's clean-up of expired links.
CREATE INDEX IF NOT EXISTS storefront_previews_store ON storefront_previews (store_id, expires_at);

ALTER TABLE storefront_previews ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_previews FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storefront_previews;
CREATE POLICY tenant_isolation ON storefront_previews
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS storefront_previews;
DROP TABLE IF EXISTS storefront_drafts;
