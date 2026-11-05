-- Stripe for the platform's billing outside Egypt (ADR-0091, approved
-- 2026-11-05 as G4 of the Stripe plan).
--
-- A checkout now records the gateway it was opened on (the payment rule
-- of its account's country, ADR-0089): its callbacks and the question
-- "is it paid" go to that gateway. Older checkouts were all Paymob's.
-- Stripe asks about a payment by its Checkout Session, kept beside
-- Paymob's order.
--
-- Expand only: a column with a constant default and a nullable column
-- (its check NOT VALID: older rows have none).

-- +goose Up
ALTER TABLE billing_checkouts ADD COLUMN IF NOT EXISTS gateway text NOT NULL DEFAULT 'paymob' CHECK (gateway ~ '^[a-z][a-z0-9_]{1,31}$'); -- migsafe:allow a constant default, no rewrite
ALTER TABLE billing_checkouts ADD COLUMN IF NOT EXISTS session_id text;
ALTER TABLE billing_checkouts ADD CONSTRAINT billing_checkouts_session_id_check CHECK (session_id ~ '^[A-Za-z0-9_]{1,255}$') NOT VALID;

-- +goose Down
ALTER TABLE billing_checkouts DROP CONSTRAINT IF EXISTS billing_checkouts_session_id_check;
ALTER TABLE billing_checkouts DROP COLUMN IF EXISTS session_id;
ALTER TABLE billing_checkouts DROP COLUMN IF EXISTS gateway;
