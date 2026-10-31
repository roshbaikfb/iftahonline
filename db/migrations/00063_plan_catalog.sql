-- The console edits the plan catalog (ADR-0056).
--
-- The console's role (app_admin) may add features, publish plan versions
-- and retire plans. It never deletes: a version, once a store bought it,
-- stays as it was sold. Merchant code (app_user) still only reads.
--
-- A subscription renews into the newest active version of its plan:
-- the renewal invoice records the version it charged for, and paying it
-- moves the subscription there (subscriptions.renewal_plan_id), so the
-- price paid and the grants received always match.
--
-- Expand only: grants and a nullable column, its FK NOT VALID.

-- +goose Up
GRANT SELECT, INSERT, UPDATE ON features, plans TO app_admin;
GRANT SELECT, INSERT ON plan_prices, plan_entitlements, plan_fees TO app_admin;

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS renewal_plan_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_renewal_plan_id_fkey
  FOREIGN KEY (renewal_plan_id) REFERENCES plans (id) NOT VALID;
CREATE INDEX IF NOT EXISTS subscriptions_renewal_plan_id_idx ON subscriptions (renewal_plan_id) WHERE renewal_plan_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS subscriptions_renewal_plan_id_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_renewal_plan_id_fkey;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS renewal_plan_id;
REVOKE SELECT, INSERT ON plan_prices, plan_entitlements, plan_fees FROM app_admin;
REVOKE SELECT, INSERT, UPDATE ON features, plans FROM app_admin;
