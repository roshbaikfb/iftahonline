-- The merchant's country and the US dollar (ADR-0088, approved
-- 2026-11-05): payments follow the merchant's country (Paymob in Egypt,
-- Stripe elsewhere), and a store may sell in dollars.
--
-- * Every currency check gains USD. The new checks are NOT VALID: every
--   existing row already passed the narrower ones; a later migration
--   validates them.
-- * stores.country is the merchant's country (ISO 3166-1 alpha-2), chosen
--   at sign-up and changed only from the console. Existing stores take it
--   from their currency. It stays nullable here (expand); the code reads
--   a missing one from the currency until a later migration makes it NOT
--   NULL.
-- * Billing itself keeps selling in EGP, SAR and AED until its dollar
--   prices and Stripe arrive (G3).

-- +goose Up
ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_currency_check;
ALTER TABLE stores ADD CONSTRAINT stores_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE variants DROP CONSTRAINT IF EXISTS variants_currency_check;
ALTER TABLE variants ADD CONSTRAINT variants_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE plan_prices DROP CONSTRAINT IF EXISTS plan_prices_currency_check;
ALTER TABLE plan_prices ADD CONSTRAINT plan_prices_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE plan_fees DROP CONSTRAINT IF EXISTS plan_fees_currency_check;
ALTER TABLE plan_fees ADD CONSTRAINT plan_fees_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE sell_fees DROP CONSTRAINT IF EXISTS sell_fees_currency_check;
ALTER TABLE sell_fees ADD CONSTRAINT sell_fees_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE billing_accounts DROP CONSTRAINT IF EXISTS billing_accounts_currency_check;
ALTER TABLE billing_accounts ADD CONSTRAINT billing_accounts_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_currency_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE billing_checkouts DROP CONSTRAINT IF EXISTS billing_checkouts_currency_check;
ALTER TABLE billing_checkouts ADD CONSTRAINT billing_checkouts_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE founding_members DROP CONSTRAINT IF EXISTS founding_members_currency_check;
ALTER TABLE founding_members ADD CONSTRAINT founding_members_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE admin_growth_mrr DROP CONSTRAINT IF EXISTS admin_growth_mrr_currency_check;
ALTER TABLE admin_growth_mrr ADD CONSTRAINT admin_growth_mrr_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;
ALTER TABLE balance_entries DROP CONSTRAINT IF EXISTS balance_entries_currency_check;
ALTER TABLE balance_entries ADD CONSTRAINT balance_entries_currency_check CHECK (currency IN ('EGP', 'SAR', 'AED', 'USD')) NOT VALID;

ALTER TABLE stores ADD COLUMN IF NOT EXISTS country char(2);
ALTER TABLE stores ADD CONSTRAINT stores_country_check CHECK (country ~ '^[A-Z]{2}$') NOT VALID;
-- FORCE RLS binds the migrating role too: it reads every store (00010's
-- directory_lookup) and updates them only for this backfill.
CREATE POLICY country_backfill ON stores FOR UPDATE TO app_owner USING (true) WITH CHECK (true);
UPDATE stores SET country = CASE currency WHEN 'EGP' THEN 'EG' WHEN 'SAR' THEN 'SA' WHEN 'AED' THEN 'AE' END WHERE country IS NULL;
DROP POLICY country_backfill ON stores;


-- +goose Down
ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_country_check;
ALTER TABLE stores DROP COLUMN IF EXISTS country;
