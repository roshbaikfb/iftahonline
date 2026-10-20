-- Storefront themes and pages (ADR-0016). A store has at most one theme
-- row and one home page; until the merchant saves them, the storefront
-- uses the defaults in code. Pages hold an ordered list of sections, each
-- validated against its JSON schema by the app before it is stored.

-- +goose Up
CREATE TABLE store_themes (
  store_id   uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  -- Colors, font, corner radius: the domain's ThemeSettings.
  settings   jsonb       NOT NULL CHECK (jsonb_typeof(settings) = 'object'),
  version    integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE store_themes ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_themes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_themes
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

CREATE TABLE store_pages (
  id          uuid        PRIMARY KEY,
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  kind        text        NOT NULL CHECK (kind IN ('home', 'landing', 'policy')),
  -- Lower-case text, not citext: lookups run under RLS (ADR-0015 §4).
  slug        text        NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 120 AND slug = lower(slug)),
  title       text        NOT NULL CHECK (char_length(title) <= 200),
  description text        NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
  -- [{"id": "...", "type": "hero", "settings": {...}}, ...] for home and landing pages.
  sections    jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(sections) = 'array'),
  -- Plain text for policies, rendered as paragraphs.
  body        text        NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
  status      text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
  version     integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT store_pages_home_slug CHECK (kind <> 'home' OR slug = 'home'),
  CONSTRAINT store_pages_policy_slug CHECK (kind <> 'policy' OR slug IN ('privacy', 'refund', 'shipping', 'terms')),
  -- Leads with store_id: covers the FK, the storefront's lookup by slug
  -- and the dashboard's list by kind.
  CONSTRAINT store_pages_store_kind_slug_key UNIQUE (store_id, kind, slug)
);

ALTER TABLE store_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_pages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_pages
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE store_pages;
DROP TABLE store_themes;
