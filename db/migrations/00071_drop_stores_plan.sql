-- Contract: stores.plan, the legacy plan of P1. Plans are billing's
-- (ADR-0045); nothing ever wrote this column (it held its default,
-- free), and since 00070's release no query reads it: stores' queries
-- list their columns. The /v1 Store.plan field stays, deprecated and
-- always free. Down brings the column back with its default.

-- +goose Up
ALTER TABLE stores DROP COLUMN IF EXISTS plan;

-- +goose Down
ALTER TABLE stores ADD COLUMN IF NOT EXISTS plan text NOT NULL DEFAULT 'free';
ALTER TABLE stores ADD CONSTRAINT stores_plan_check CHECK (plan IN ('free', 'basic', 'pro')) NOT VALID;
