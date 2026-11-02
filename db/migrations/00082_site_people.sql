-- The about page and the site's identity from the console (S4b-1,
-- ADR-0073): the founding story, the video, the logo and the share
-- image; the founders and the team; merchants' real reviews.
--
-- Platform data, like site_settings: the console writes as app_admin,
-- the storefront role reads as app_user. Images are masters the site
-- module re-encoded under platform/site/ (never SVG).
--
-- Expand only: new columns with defaults, new tables.

-- +goose Up
ALTER TABLE site_settings
  ADD COLUMN story_ar text NOT NULL DEFAULT '' CHECK (char_length(story_ar) <= 4000),
  ADD COLUMN story_en text NOT NULL DEFAULT '' CHECK (char_length(story_en) <= 4000),
  -- A YouTube video's ID; '' shows none.
  ADD COLUMN video_id text NOT NULL DEFAULT '' CHECK (video_id = '' OR video_id ~ '^[A-Za-z0-9_-]{11}$'),
  ADD COLUMN logo_key text NOT NULL DEFAULT '' CHECK (logo_key = '' OR logo_key ~ '^platform/site/[0-9A-Z]{26}\.(jpeg|png)$'),
  ADD COLUMN og_key   text NOT NULL DEFAULT '' CHECK (og_key = '' OR og_key ~ '^platform/site/[0-9A-Z]{26}\.(jpeg|png)$');

CREATE TABLE site_people (
  id           uuid        PRIMARY KEY,
  kind         text        NOT NULL CHECK (kind IN ('founder', 'team')),
  position     int         NOT NULL CHECK (position >= 0),
  name_ar      text        NOT NULL CHECK (char_length(name_ar) BETWEEN 2 AND 80),
  name_en      text        NOT NULL DEFAULT '' CHECK (char_length(name_en) <= 80),
  role_ar      text        NOT NULL CHECK (char_length(role_ar) BETWEEN 2 AND 80),
  role_en      text        NOT NULL DEFAULT '' CHECK (char_length(role_en) <= 80),
  bio_ar       text        NOT NULL DEFAULT '' CHECK (char_length(bio_ar) <= 600),
  bio_en       text        NOT NULL DEFAULT '' CHECK (char_length(bio_en) <= 600),
  photo_key    text        NOT NULL DEFAULT '' CHECK (photo_key = '' OR photo_key ~ '^platform/site/[0-9A-Z]{26}\.(jpeg|png)$'),
  linkedin_url text        NOT NULL DEFAULT '' CHECK (linkedin_url = '' OR (linkedin_url ~ '^https://' AND char_length(linkedin_url) <= 300)),
  x_url        text        NOT NULL DEFAULT '' CHECK (x_url = '' OR (x_url ~ '^https://' AND char_length(x_url) <= 300)),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
-- The site and the console read them by kind, in order.
CREATE INDEX site_people_order ON site_people (kind, position, created_at);

CREATE TABLE site_testimonials (
  id         uuid        PRIMARY KEY,
  position   int         NOT NULL CHECK (position >= 0),
  name       text        NOT NULL CHECK (char_length(name) BETWEEN 2 AND 80),
  store_name text        NOT NULL DEFAULT '' CHECK (char_length(store_name) <= 80),
  store_url  text        NOT NULL DEFAULT '' CHECK (store_url = '' OR (store_url ~ '^https://' AND char_length(store_url) <= 300)),
  quote_ar   text        NOT NULL CHECK (char_length(quote_ar) BETWEEN 10 AND 600),
  quote_en   text        NOT NULL DEFAULT '' CHECK (char_length(quote_en) <= 600),
  photo_key  text        NOT NULL DEFAULT '' CHECK (photo_key = '' OR photo_key ~ '^platform/site/[0-9A-Z]{26}\.(jpeg|png)$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX site_testimonials_order ON site_testimonials (position, created_at);

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON site_people, site_testimonials FROM app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON site_people, site_testimonials TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS site_testimonials;
DROP TABLE IF EXISTS site_people;
ALTER TABLE site_settings DROP COLUMN IF EXISTS og_key, DROP COLUMN IF EXISTS logo_key, DROP COLUMN IF EXISTS video_id,
  DROP COLUMN IF EXISTS story_en, DROP COLUMN IF EXISTS story_ar;
