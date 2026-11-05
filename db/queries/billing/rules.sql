-- name: ListPaymentRules :many
-- Payment rules by the merchant's country (ADR-0089); ZZ is the others.
SELECT country, billing_gateway, store_gateways FROM payment_rules ORDER BY country = 'ZZ', country;

-- name: SavePaymentRule :exec
INSERT INTO payment_rules (country, billing_gateway, store_gateways) VALUES (@country, @billing_gateway, @store_gateways)
ON CONFLICT (country) DO UPDATE SET billing_gateway = excluded.billing_gateway, store_gateways = excluded.store_gateways, updated_at = now();

-- name: DeletePaymentRule :execrows
DELETE FROM payment_rules WHERE country = @country AND country <> 'ZZ';

-- name: ListTaxes :many
SELECT country, rate_bps, name_ar, name_en FROM tax_rates ORDER BY country = 'ZZ', country;

-- name: SaveTax :exec
INSERT INTO tax_rates (country, rate_bps, name_ar, name_en) VALUES (@country, @rate_bps, @name_ar, @name_en)
ON CONFLICT (country) DO UPDATE SET rate_bps = excluded.rate_bps, name_ar = excluded.name_ar, name_en = excluded.name_en;

-- name: DeleteTax :execrows
DELETE FROM tax_rates WHERE country = @country AND country <> 'ZZ';

-- name: ListFXRates :many
-- Each store currency's rate to the dollar (ADR-0090).
SELECT currency::text, per_usd_micros, updated_at FROM fx_rates ORDER BY currency;

-- name: SaveFXRate :exec
-- The console's (app_admin).
INSERT INTO fx_rates (currency, per_usd_micros) VALUES (@currency, @per_usd_micros)
ON CONFLICT (currency) DO UPDATE SET per_usd_micros = excluded.per_usd_micros, updated_at = now();
