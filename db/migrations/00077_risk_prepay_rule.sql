-- A third risk rule (ADR-0067): from which band a storefront cash
-- order's shopper pays the delivery upfront by card. Off by default, so
-- every store's rules stay what they were.

-- +goose Up
ALTER TABLE risk_settings ADD COLUMN IF NOT EXISTS prepay_from text NOT NULL DEFAULT 'off';
ALTER TABLE risk_settings ADD CONSTRAINT risk_settings_prepay_from CHECK (prepay_from IN ('off', 'medium', 'high')) NOT VALID;

-- +goose Down
ALTER TABLE risk_settings DROP CONSTRAINT IF EXISTS risk_settings_prepay_from;
ALTER TABLE risk_settings DROP COLUMN IF EXISTS prepay_from;
