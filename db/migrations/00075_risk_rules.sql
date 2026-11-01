-- The store's risk rules (ADR-0066): from which band a storefront cash
-- order is held for review, and from which band the customer's own "yes"
-- must be verified by phone. 'off' does nothing; a store without a row
-- has both off. The rules are a plan feature, risk_rules (the bands are
-- free): every paid plan has it, the free plan does not. The console can
-- change that like any feature (ADR-0056).

-- +goose Up
CREATE TABLE risk_settings (
  store_id    uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  hold_from   text        NOT NULL DEFAULT 'off' CHECK (hold_from IN ('off', 'medium', 'high')),
  verify_from text        NOT NULL DEFAULT 'off' CHECK (verify_from IN ('off', 'medium', 'high')),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE risk_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON risk_settings
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON risk_settings FROM app_user;

INSERT INTO features (key, kind, name_ar, name_en, sort) VALUES
  ('risk_rules', 'flag', 'حماية الدفع عند الاستلام', 'Cash-on-delivery protection rules', 110)
ON CONFLICT (key) DO NOTHING;

INSERT INTO plan_entitlements (plan_id, feature, value)
SELECT p.id, 'risk_rules', CASE WHEN p.kind = 'free' THEN 0 ELSE 1 END
FROM plans p
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM plan_entitlements WHERE feature = 'risk_rules';
DELETE FROM features WHERE key = 'risk_rules';
DROP TABLE IF EXISTS risk_settings;
