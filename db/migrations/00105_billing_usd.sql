-- Billing in dollars outside Egypt (ADR-0090, approved 2026-11-05).
--
-- * fx_rates: each store currency's rate to the dollar, set from the
--   console (units per dollar × 10^6). The riyal and the dirham are seeded
--   at their pegs; the pound is the console's to set.
-- * plan_fees.fixed_minor: a fixed amount per delivered order beside the
--   rate (EasyOrders' $0.04 a order).
-- * sell_fees keeps a fee's order currency, goods and rate when its order
--   was in another currency than its account's (nullable: older fees were
--   in the account's own).
-- * billing_settings gains the dollar's minimum ($10).
-- * Every plan priced in riyals gains dollar prices and fees at 3.75, to
--   the cent, so that Gulf merchants have offers before the console sets
--   its own. The riyal's and the dirham's stay for the accounts billed in
--   them.
--
-- Expand only: a new table, nullable columns or columns with a constant
-- default, rows.

-- +goose Up
CREATE TABLE fx_rates (
  currency       char(3)     PRIMARY KEY CHECK (currency IN ('EGP', 'SAR', 'AED')),
  per_usd_micros bigint      NOT NULL CHECK (per_usd_micros > 0 AND per_usd_micros <= 1000000000000),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
INSERT INTO fx_rates (currency, per_usd_micros) VALUES ('SAR', 3750000), ('AED', 3672500);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON fx_rates FROM app_user;
GRANT SELECT, INSERT, UPDATE ON fx_rates TO app_admin;

ALTER TABLE plan_fees ADD COLUMN IF NOT EXISTS fixed_minor bigint NOT NULL DEFAULT 0 CHECK (fixed_minor >= 0); -- migsafe:allow plan rows (tens): a constant default, no rewrite

ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS fixed_minor bigint NOT NULL DEFAULT 0;
ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS order_currency char(3);
ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS order_base_minor bigint;
ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS rate_micros bigint;
ALTER TABLE sell_fees ADD CONSTRAINT sell_fees_conversion_check
  CHECK ((order_currency IS NULL) = (order_base_minor IS NULL) AND (order_currency IS NULL) = (rate_micros IS NULL) AND (rate_micros IS NULL OR rate_micros > 0)) NOT VALID;

ALTER TABLE billing_settings ADD COLUMN IF NOT EXISTS minimum_usd_minor bigint NOT NULL DEFAULT 1000 CHECK (minimum_usd_minor BETWEEN 100 AND 100000000); -- migsafe:allow one row

INSERT INTO plan_prices (plan_id, currency, cycle, amount_minor)
SELECT plan_id, 'USD', cycle, round(amount_minor / 3.75)::bigint FROM plan_prices WHERE currency = 'SAR'
ON CONFLICT DO NOTHING;
INSERT INTO plan_fees (plan_id, currency, rate_bps, min_minor, max_minor)
SELECT plan_id, 'USD', rate_bps, round(min_minor / 3.75)::bigint, round(max_minor / 3.75)::bigint FROM plan_fees WHERE currency = 'SAR'
ON CONFLICT DO NOTHING;

-- An agency's country, for the console's list: the function gains a
-- column (callers name theirs, so older code reads it unchanged).
DROP FUNCTION IF EXISTS app.agency_accounts();
-- +goose StatementBegin
CREATE FUNCTION app.agency_accounts()
  RETURNS TABLE (id uuid, name text, currency text, plan_code text, created_at timestamptz, stores bigint, country text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT a.id, a.name, a.currency::text, a.plan_code, a.created_at,
         (SELECT count(*) FROM public.billing_account_stores m WHERE m.account_id = a.id), a.country::text
  FROM public.billing_accounts a
  WHERE a.kind = 'agency'
  ORDER BY a.created_at DESC, a.id
  LIMIT 1000
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.agency_accounts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.agency_accounts() TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.agency_accounts();
-- +goose StatementBegin
CREATE FUNCTION app.agency_accounts()
  RETURNS TABLE (id uuid, name text, currency text, plan_code text, created_at timestamptz, stores bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT a.id, a.name, a.currency::text, a.plan_code, a.created_at,
         (SELECT count(*) FROM public.billing_account_stores m WHERE m.account_id = a.id)
  FROM public.billing_accounts a
  WHERE a.kind = 'agency'
  ORDER BY a.created_at DESC, a.id
  LIMIT 1000
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.agency_accounts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.agency_accounts() TO app_user;
DELETE FROM plan_fees WHERE currency = 'USD';
DELETE FROM plan_prices WHERE currency = 'USD';
ALTER TABLE billing_settings DROP COLUMN IF EXISTS minimum_usd_minor;
ALTER TABLE sell_fees DROP CONSTRAINT IF EXISTS sell_fees_conversion_check;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS rate_micros;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS order_base_minor;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS order_currency;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS fixed_minor;
ALTER TABLE plan_fees DROP COLUMN IF EXISTS fixed_minor;
DROP TABLE IF EXISTS fx_rates;
