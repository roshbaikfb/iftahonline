-- Prepaid shipping (ADR-0067): a cash-on-delivery order whose customer
-- the store's risk rules want to pay the delivery upfront. The deposit
-- is the shipping and the COD fee, paid by card on the gateway's page;
-- the courier collects the rest (total − deposit). deposit_status follows
-- the gateway like a card order's payment_status: awaiting, paid,
-- partially_refunded, refunded. Both are zero/null for every other order.
--
-- Expand only: a default, a nullable column, CHECKs NOT VALID (migsafe).

-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS deposit_minor bigint NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS deposit_status text;
ALTER TABLE orders ADD CONSTRAINT orders_deposit CHECK (
  deposit_minor >= 0 AND deposit_minor <= total_minor AND (deposit_minor = 0) = (deposit_status IS NULL)) NOT VALID;
ALTER TABLE orders ADD CONSTRAINT orders_deposit_status CHECK (
  deposit_status IN ('awaiting', 'paid', 'partially_refunded', 'refunded')) NOT VALID;
-- A risk action of its own: the rules asked for the deposit.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_risk_action;
ALTER TABLE orders ADD CONSTRAINT orders_risk_action CHECK (risk_action IN ('hold', 'verify', 'released', 'prepay')) NOT VALID;

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_risk_action;
ALTER TABLE orders ADD CONSTRAINT orders_risk_action CHECK (risk_action IN ('hold', 'verify', 'released')) NOT VALID;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_deposit_status;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_deposit;
ALTER TABLE orders DROP COLUMN IF EXISTS deposit_status;
ALTER TABLE orders DROP COLUMN IF EXISTS deposit_minor;
