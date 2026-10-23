-- Shipping zones, rates and fees (ADR-0025).
--
-- A store groups governorates (platform/geo codes) into zones. A zone has
-- a flat rate, optional per-city rates (matched on the city's search
-- form), and may override the store's free-shipping threshold and
-- cash-on-delivery fee. A governorate is in at most one zone: the
-- primary key of shipping_zone_areas says so. A store with no zones
-- delivers everywhere with free shipping, as before zones existed.
--
-- Amounts are minor units of the store's currency.

-- +goose Up
CREATE TABLE shipping_settings (
  store_id        uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  cod_fixed_minor bigint      NOT NULL DEFAULT 0 CHECK (cod_fixed_minor BETWEEN 0 AND 1000000000),
  cod_bp          integer     NOT NULL DEFAULT 0 CHECK (cod_bp BETWEEN 0 AND 1000),
  cod_min_minor   bigint      NOT NULL DEFAULT 0 CHECK (cod_min_minor BETWEEN 0 AND 1000000000),
  cod_max_minor   bigint      NOT NULL DEFAULT 0 CHECK (cod_max_minor BETWEEN 0 AND 1000000000),
  free_over_minor bigint      CHECK (free_over_minor BETWEEN 0 AND 1000000000),
  version         integer     NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE shipping_zones (
  id              uuid        PRIMARY KEY,
  store_id        uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  name            text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
  rate_minor      bigint      NOT NULL CHECK (rate_minor BETWEEN 0 AND 1000000000),
  free_over_minor bigint      CHECK (free_over_minor BETWEEN 0 AND 1000000000),
  -- The zone's own COD fee: all four set, or none (the store's applies).
  cod_fixed_minor bigint      CHECK (cod_fixed_minor BETWEEN 0 AND 1000000000),
  cod_bp          integer     CHECK (cod_bp BETWEEN 0 AND 1000),
  cod_min_minor   bigint      CHECK (cod_min_minor BETWEEN 0 AND 1000000000),
  cod_max_minor   bigint      CHECK (cod_max_minor BETWEEN 0 AND 1000000000),
  position        integer     NOT NULL CHECK (position >= 0),
  version         integer     NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT shipping_zones_cod_fee CHECK (num_nulls(cod_fixed_minor, cod_bp, cod_min_minor, cod_max_minor) IN (0, 4)),
  CONSTRAINT shipping_zones_store_id_key UNIQUE (store_id, id)
);
-- Leads with store_id (covers the FK); the store's zones in order.
CREATE INDEX shipping_zones_store_position ON shipping_zones (store_id, position, id);

CREATE TABLE shipping_zone_areas (
  store_id uuid NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  zone_id  uuid NOT NULL,
  area     text NOT NULL CHECK (area ~ '^[A-Z]{2}-[A-Z0-9]{1,3}$'),
  -- One zone per governorate in a store.
  PRIMARY KEY (store_id, area),
  CONSTRAINT shipping_zone_areas_zone_fkey FOREIGN KEY (store_id, zone_id)
    REFERENCES shipping_zones (store_id, id) ON DELETE CASCADE
);
CREATE INDEX shipping_zone_areas_store_zone ON shipping_zone_areas (store_id, zone_id);

CREATE TABLE shipping_city_rates (
  store_id   uuid   NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  zone_id    uuid   NOT NULL,
  city_key   text   NOT NULL CHECK (char_length(city_key) BETWEEN 1 AND 320),
  city_name  text   NOT NULL CHECK (char_length(city_name) BETWEEN 2 AND 80),
  rate_minor bigint NOT NULL CHECK (rate_minor BETWEEN 0 AND 1000000000),
  PRIMARY KEY (store_id, zone_id, city_key),
  CONSTRAINT shipping_city_rates_zone_fkey FOREIGN KEY (store_id, zone_id)
    REFERENCES shipping_zones (store_id, id) ON DELETE CASCADE
);

ALTER TABLE shipping_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipping_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipping_settings
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE shipping_zones ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipping_zones FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipping_zones
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE shipping_zone_areas ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipping_zone_areas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipping_zone_areas
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE shipping_city_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipping_city_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipping_city_rates
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE shipping_city_rates;
DROP TABLE shipping_zone_areas;
DROP TABLE shipping_zones;
DROP TABLE shipping_settings;
