-- Why an order did not reach its customer (ADR-0065): refused, no_answer
-- (set by a person moving it to cancelled, failed_delivery or returned)
-- or unconfirmed (the confirmation message went unanswered). It belongs
-- to the latest move: every move sets it, most to null. The risk module
-- learns phones' behaviour from it.
--
-- Expand only: nullable, so old code that never writes it keeps working.

-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS outcome_reason text;
ALTER TABLE orders ADD CONSTRAINT orders_outcome_reason CHECK (
  outcome_reason IN ('refused', 'no_answer', 'unconfirmed')) NOT VALID;

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_outcome_reason;
ALTER TABLE orders DROP COLUMN IF EXISTS outcome_reason;
