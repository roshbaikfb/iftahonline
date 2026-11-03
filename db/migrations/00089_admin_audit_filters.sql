-- The admin log's store filter (the console's store page shows what was
-- done to the store): its own actions, and its invoices', whose metadata
-- names it. The log is small and append-only, so a plain build is fine.

-- +goose Up
CREATE INDEX IF NOT EXISTS admin_audit_log_store_idx ON admin_audit_log (target_id, occurred_at DESC, id DESC) WHERE target_type = 'store';
CREATE INDEX IF NOT EXISTS admin_audit_log_meta_store_idx ON admin_audit_log ((metadata ->> 'store'), occurred_at DESC, id DESC) WHERE metadata ? 'store';

-- +goose Down
DROP INDEX IF EXISTS admin_audit_log_meta_store_idx;
DROP INDEX IF EXISTS admin_audit_log_store_idx;
