-- Hidden variants (ADR-0035 §7): the merchant takes a variant off sale
-- without losing it or its stock, and can put it back. A hidden variant
-- is not sold, not reserved and not shown to shoppers.
--
-- Expand only: a column with a constant default (no table rewrite).

-- +goose Up
ALTER TABLE variants ADD COLUMN IF NOT EXISTS hidden boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE variants DROP COLUMN IF EXISTS hidden;
