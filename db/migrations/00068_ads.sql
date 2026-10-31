-- Ads in free stores (ADR-0062, approved 2026-10-31).
--
-- Platform data, owned by the ads module, no store_id: one settings row
-- (Google AdSense's publisher and a unit per placement), the platform's
-- own banners, and their clicks counted per day (clicks only: no
-- impressions, no visitor). The console writes settings and banners as
-- app_admin; the storefront (app_user) reads them and counts clicks.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE ad_settings (
  id              int         PRIMARY KEY CHECK (id = 1),
  adsense_enabled boolean     NOT NULL DEFAULT false,
  publisher_id    text        NOT NULL DEFAULT '' CHECK (publisher_id = '' OR publisher_id ~ '^ca-pub-[0-9]{16}$'),
  units           jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(units) = 'object'),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (NOT adsense_enabled OR publisher_id <> '')
);
INSERT INTO ad_settings (id) VALUES (1);

CREATE TABLE ad_banners (
  id         uuid        PRIMARY KEY,
  title      text        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
  image_key  text        NOT NULL CHECK (image_key ~ '^platform/ads/[0-9A-Z]{26}\.(jpeg|png)$'),
  width      int         NOT NULL CHECK (width > 0),
  height     int         NOT NULL CHECK (height > 0),
  link_url   text        NOT NULL CHECK (link_url ~ '^https://' AND char_length(link_url) <= 500),
  placement  text        NOT NULL CHECK (placement IN ('home_top', 'grid', 'category_top', 'product_bottom')),
  country    char(2)     CHECK (country IN ('EG', 'SA', 'AE')), -- NULL: every country
  starts_at  timestamptz NOT NULL,
  ends_at    timestamptz CHECK (ends_at IS NULL OR ends_at > starts_at),
  weight     int         NOT NULL DEFAULT 10 CHECK (weight BETWEEN 1 AND 100),
  active     boolean     NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- The storefront reads the live ones of a placement.
CREATE INDEX ad_banners_live ON ad_banners (placement, starts_at) WHERE active;

CREATE TABLE ad_clicks (
  banner_id uuid   NOT NULL REFERENCES ad_banners (id) ON DELETE CASCADE,
  day       date   NOT NULL,
  clicks    bigint NOT NULL DEFAULT 0 CHECK (clicks >= 0),
  -- Leads with banner_id, so it also covers the FK.
  PRIMARY KEY (banner_id, day)
);

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON ad_settings, ad_banners FROM app_user;
REVOKE DELETE, TRUNCATE ON ad_clicks FROM app_user;
GRANT SELECT, UPDATE ON ad_settings TO app_admin;
GRANT SELECT, INSERT, UPDATE ON ad_banners TO app_admin;
GRANT SELECT ON ad_clicks TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS ad_clicks;
DROP TABLE IF EXISTS ad_banners;
DROP TABLE IF EXISTS ad_settings;
