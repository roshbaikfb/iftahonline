-- A voided month's invoice no longer holds its period (ADR-0087 §7): an
-- agency's invoice voided when some of its stores move to pay as you
-- sell is written again for the stores that stay. invoices_monthly_key
-- becomes one live (not void) monthly invoice per account and period.
--
-- Built CONCURRENTLY outside a transaction, so writes are not blocked;
-- the old index goes once the new one exists.

-- +goose NO TRANSACTION
-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS invoices_monthly_live_key ON invoices (account_id, period)
  WHERE purpose = 'monthly' AND status <> 'void';
DROP INDEX CONCURRENTLY IF EXISTS invoices_monthly_key;

-- +goose Down
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS invoices_monthly_key ON invoices (account_id, period) WHERE purpose = 'monthly';
DROP INDEX CONCURRENTLY IF EXISTS invoices_monthly_live_key;
