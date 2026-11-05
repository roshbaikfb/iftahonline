-- Payment rules and taxes by country, from the console (ADR-0089,
-- approved 2026-11-05).
--
-- payment_rules says, for a merchant's country, the gateway the platform
-- bills it through and the gateways its storefront may take cards with
-- (cash on delivery always). ZZ (CLDR's unknown region) is every other
-- country. Seeded with the approved rule: Egypt through Paymob, the rest
-- through Stripe.
--
-- tax_rates becomes any country's, with a ZZ row for the rest (0 until
-- the console sets it). Accounts may be in any country (the GCC beyond
-- Saudi Arabia and the UAE, then the world).
--
-- Platform data the console writes as app_admin; merchants' code reads it.
-- Expand only: a new table, rows, checks replaced NOT VALID.

-- +goose Up
CREATE TABLE payment_rules (
  country         char(2)     PRIMARY KEY CHECK (country ~ '^[A-Z]{2}$'),
  billing_gateway text        NOT NULL CHECK (billing_gateway IN ('paymob', 'stripe')),
  store_gateways  text[]      NOT NULL DEFAULT '{}' CHECK (store_gateways <@ ARRAY['paymob', 'stripe']::text[]),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
INSERT INTO payment_rules (country, billing_gateway, store_gateways) VALUES
  ('EG', 'paymob', ARRAY['paymob']),
  ('ZZ', 'stripe', ARRAY['stripe']);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON payment_rules FROM app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON payment_rules TO app_admin;

ALTER TABLE tax_rates DROP CONSTRAINT IF EXISTS tax_rates_country_check;
ALTER TABLE tax_rates ADD CONSTRAINT tax_rates_country_check CHECK (country ~ '^[A-Z]{2}$') NOT VALID;
INSERT INTO tax_rates (country, rate_bps, name_ar, name_en) VALUES ('ZZ', 0, 'الضريبة', 'Tax') ON CONFLICT DO NOTHING;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON tax_rates FROM app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON tax_rates TO app_admin;

ALTER TABLE billing_accounts DROP CONSTRAINT IF EXISTS billing_accounts_country_check;
ALTER TABLE billing_accounts ADD CONSTRAINT billing_accounts_country_check CHECK (country ~ '^[A-Z]{2}$') NOT VALID;

-- +goose Down
REVOKE SELECT, INSERT, UPDATE, DELETE ON tax_rates FROM app_admin;
DELETE FROM tax_rates WHERE country = 'ZZ';
DROP TABLE IF EXISTS payment_rules;
