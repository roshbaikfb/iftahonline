-- Per-store feature flags (ADR-0057).
--
-- The console may change what one store is granted of a feature, above
-- or below its plan: a row here replaces the plan's grant of that
-- feature for that store, until expires_at (NULL: until cleared). value
-- NULL is unlimited, like plan_entitlements. The entitlements module
-- owns the table and merges it over the plan's grants; billing is not
-- involved.
--
-- Only features the code enforces are overridden (a display-only key
-- changes nothing), so the feature column needs no FK to the catalog's
-- table, which another module owns. The reason is the admin's, shown in
-- the console.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE entitlement_overrides (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  feature    text        NOT NULL CHECK (feature ~ '^[a-z][a-z_]{0,31}$'),
  value      bigint      CHECK (value IS NULL OR value >= 0),
  expires_at timestamptz,
  reason     text        NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id, so it also covers the FK.
  PRIMARY KEY (store_id, feature)
);

ALTER TABLE entitlement_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON entitlement_overrides
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
-- Clearing an override deletes its row; no TRUNCATE.
REVOKE TRUNCATE ON entitlement_overrides FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS entitlement_overrides;
