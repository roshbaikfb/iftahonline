-- The platform site's guides (S4c, ADR-0073): articles written in the
-- console in Markdown, Arabic first with an optional English version,
-- published at /guides/{slug}.
--
-- Platform data, like the rest of the site's content: the console
-- writes as app_admin, the storefront role reads as app_user.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE site_guides (
  id           uuid        PRIMARY KEY,
  slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 80),
  status       text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
  title_ar     text        NOT NULL CHECK (char_length(title_ar) BETWEEN 5 AND 120),
  summary_ar   text        NOT NULL CHECK (char_length(summary_ar) BETWEEN 20 AND 300),
  body_ar      text        NOT NULL CHECK (char_length(body_ar) BETWEEN 50 AND 40000),
  -- English is optional, as one: without it the guide is Arabic only.
  title_en     text        NOT NULL DEFAULT '' CHECK (char_length(title_en) <= 120),
  summary_en   text        NOT NULL DEFAULT '' CHECK (char_length(summary_en) <= 300),
  body_en      text        NOT NULL DEFAULT '' CHECK (char_length(body_en) <= 40000),
  cover_key    text        NOT NULL DEFAULT '' CHECK (cover_key = '' OR cover_key ~ '^platform/site/[0-9A-Z]{26}\.(jpeg|png)$'),
  -- Set when first published, kept when unpublished and published again.
  published_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT site_guides_slug_key UNIQUE (slug),
  CHECK ((title_en = '') = (body_en = '') AND (title_en = '') = (summary_en = '')),
  CHECK (status = 'draft' OR published_at IS NOT NULL)
);
-- The site lists the published, newest first.
CREATE INDEX site_guides_published ON site_guides (published_at DESC) WHERE status = 'published';

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON site_guides FROM app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON site_guides TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS site_guides;
