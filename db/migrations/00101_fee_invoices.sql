-- The card with a minimum (ADR-0086, approved 2026-11-04): an account
-- that pays its pay-as-you-sell fees by card is invoiced as soon as its
-- due fees reach the minimum, besides the month's invoice. Such an
-- invoice has purpose 'fees': it is charged by the dunning like any
-- other, and an account may have several in a month (only 'monthly'
-- invoices are one per account and period, invoices_monthly_key).
--
-- Expand only: the purpose check replaced NOT VALID.

-- +goose Up
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_purpose_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_purpose_check CHECK (purpose IN ('monthly', 'subscribe', 'fees')) NOT VALID;

-- +goose Down
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_purpose_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_purpose_check CHECK (purpose IN ('monthly', 'subscribe')) NOT VALID;
