-- The store's checkout payment preference (ADR-0036): which method the
-- checkout page starts on. Cash on delivery unless the merchant picks
-- card, and card only counts while a gateway is connected.
--
-- Expand only: a new table; no row means cash on delivery.

-- +goose Up
CREATE TABLE IF NOT EXISTS payment_settings (
  store_id       uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  default_method text        NOT NULL DEFAULT 'cod' CHECK (default_method IN ('cod', 'card')),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE payment_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON payment_settings;
CREATE POLICY tenant_isolation ON payment_settings
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS payment_settings;
