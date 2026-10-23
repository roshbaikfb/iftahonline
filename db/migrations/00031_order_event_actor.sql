-- Order moves made by the system (ADR-0027): a carrier's tracking moves
-- an order to shipped, delivered, failed_delivery or returned. Such an
-- event has no user; `actor` names who made it ("carrier:bosta").
-- Expand only: old writers leave it empty.

-- +goose Up
ALTER TABLE order_events ADD COLUMN actor text NOT NULL DEFAULT '' CHECK (actor ~ '^([a-z]+:[a-z0-9_]{1,32})?$');

-- +goose Down
ALTER TABLE order_events DROP COLUMN actor;
