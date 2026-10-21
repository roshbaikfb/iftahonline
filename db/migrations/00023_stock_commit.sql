-- Orders hold, commit and return stock (ADR-0011 §8, ADR-0020).
--
-- * A held reservation has no expiry (expires_at NULL): a cash-on-delivery
--   order stays pending until the merchant confirms it, which can take
--   days. No expiry job is scheduled for it.
-- * committed: the units left the warehouse (the order shipped);
--   inventory on_hand and reserved both dropped by qty.
-- * returned: committed units came back (the order was returned);
--   on_hand rose by qty again.
-- Expand only: old code never writes the new states or a NULL expiry.

-- +goose Up
ALTER TABLE stock_reservations ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE stock_reservations ADD COLUMN committed_at timestamptz;
ALTER TABLE stock_reservations ADD COLUMN returned_at timestamptz;
ALTER TABLE stock_reservations DROP CONSTRAINT stock_reservations_status_check;
ALTER TABLE stock_reservations ADD CONSTRAINT stock_reservations_status_check
  CHECK (status IN ('active', 'released', 'expired', 'committed', 'returned')) NOT VALID;
ALTER TABLE stock_reservations VALIDATE CONSTRAINT stock_reservations_status_check;

-- +goose Down
DELETE FROM stock_reservations WHERE status IN ('committed', 'returned') OR expires_at IS NULL;
ALTER TABLE stock_reservations DROP CONSTRAINT stock_reservations_status_check;
ALTER TABLE stock_reservations ADD CONSTRAINT stock_reservations_status_check
  CHECK (status IN ('active', 'released', 'expired'));
ALTER TABLE stock_reservations DROP COLUMN returned_at;
ALTER TABLE stock_reservations DROP COLUMN committed_at;
ALTER TABLE stock_reservations ALTER COLUMN expires_at SET NOT NULL;
