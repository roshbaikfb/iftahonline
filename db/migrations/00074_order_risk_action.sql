-- What a store's risk rules did to an order when it was placed
-- (ADR-0066), and the customer's band at that moment:
--   hold     the order waits for the merchant's review: no confirmation
--            message, and it cannot be confirmed until released;
--   verify   the customer's own "yes" does not confirm it: the merchant
--            calls first;
--   released a hold the merchant lifted.
-- Both are null for orders the rules did not touch, and for every order
-- placed before them.
--
-- Expand only: nullable, CHECKs NOT VALID (migsafe).

-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS risk_action text;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS risk_band text;
ALTER TABLE orders ADD CONSTRAINT orders_risk_action CHECK (risk_action IN ('hold', 'verify', 'released')) NOT VALID;
ALTER TABLE orders ADD CONSTRAINT orders_risk_band CHECK (risk_band IN ('new', 'low', 'medium', 'high')) NOT VALID;

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_risk_band;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_risk_action;
ALTER TABLE orders DROP COLUMN IF EXISTS risk_band;
ALTER TABLE orders DROP COLUMN IF EXISTS risk_action;
