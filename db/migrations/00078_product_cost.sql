-- What a variant costs the merchant (ADR-0068), for the profit
-- dashboard: variants.cost_minor, in the variant's currency; null is not
-- known yet. Each order line keeps the cost at the time it was placed
-- (order_items.unit_cost_minor), so a later change does not rewrite
-- past profit.
--
-- Expand only: nullable columns, CHECKs NOT VALID (migsafe).

-- +goose Up
ALTER TABLE variants ADD COLUMN IF NOT EXISTS cost_minor bigint;
ALTER TABLE variants ADD CONSTRAINT variants_cost_minor CHECK (cost_minor BETWEEN 0 AND 1000000000000) NOT VALID;
ALTER TABLE order_items ADD COLUMN IF NOT EXISTS unit_cost_minor bigint;
ALTER TABLE order_items ADD CONSTRAINT order_items_unit_cost CHECK (unit_cost_minor >= 0) NOT VALID;

-- +goose Down
ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_unit_cost;
ALTER TABLE order_items DROP COLUMN IF EXISTS unit_cost_minor;
ALTER TABLE variants DROP CONSTRAINT IF EXISTS variants_cost_minor;
ALTER TABLE variants DROP COLUMN IF EXISTS cost_minor;
