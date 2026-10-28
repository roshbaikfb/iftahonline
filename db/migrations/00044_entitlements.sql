-- Per-store quotas (ADR-0038). Until plans arrive (P7) every store gets
-- the same monthly limits from the environment; this table only counts
-- what each store used per feature and calendar month (UTC).
--
-- A consumption is one conditional upsert: the row lock serializes a
-- store's concurrent senders, and the WHERE keeps used <= limit, so the
-- last unit cannot be spent twice. It runs in the caller's transaction,
-- so a rolled-back send never counts.
--
-- One row per store, feature and month: small, kept.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE IF NOT EXISTS usage_counters (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  feature    text        NOT NULL CHECK (feature ~ '^[a-z][a-z_]{0,31}$'),
  period     date        NOT NULL CHECK (extract(day FROM period) = 1),
  used       bigint      NOT NULL DEFAULT 0 CHECK (used >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id, so it also covers the FK.
  PRIMARY KEY (store_id, feature, period)
);

ALTER TABLE usage_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_counters FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON usage_counters;
CREATE POLICY tenant_isolation ON usage_counters
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON usage_counters FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS usage_counters;
