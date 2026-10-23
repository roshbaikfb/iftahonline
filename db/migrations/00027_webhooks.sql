-- Inbound webhooks from carriers and payment gateways (ADR-0024).
--
-- A webhook is accepted only after its signature is verified against the
-- store's connection. Accepting writes, in one transaction:
--   * a receipt, unique per (store, provider, provider event id), so a
--     replayed delivery is recognised and processed once;
--   * the raw body, as received, for audits and reprocessing;
--   * the provider module's processing job (transactional outbox), which
--     re-queries the provider before it changes any state.
-- Forged deliveries are refused and logged; nothing is stored for them.
--
-- Receipts are small and kept 90 days (each new receipt purges a few of
-- its store's expired ones: FORCE RLS binds the owner too, so a global
-- purge would see nothing). Bodies are the bulk and live in monthly
-- partitions that the platform job drops after 3 whole months. A
-- receipt's ID is a ULID whose time is received_at, so its body is found
-- in one partition.

-- +goose Up
CREATE TABLE webhook_receipts (
  id            uuid        PRIMARY KEY,
  store_id      uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  provider      text        NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$'),
  connection_id uuid        NOT NULL,
  event_id      text        NOT NULL CHECK (char_length(event_id) BETWEEN 1 AND 200),
  received_at   timestamptz NOT NULL,
  processed_at  timestamptz,
  outcome       text        NOT NULL DEFAULT '' CHECK (outcome ~ '^[a-z_]{0,64}$'),
  -- The once-only key; leads with store_id, so it also covers the FK.
  CONSTRAINT webhook_receipts_event_key UNIQUE (store_id, provider, event_id)
);
-- The per-store retention purge.
CREATE INDEX webhook_receipts_store_received ON webhook_receipts (store_id, received_at);

CREATE TABLE webhook_bodies (
  store_id     uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  received_at  timestamptz NOT NULL,
  receipt_id   uuid        NOT NULL,
  content_type text        NOT NULL DEFAULT '' CHECK (char_length(content_type) <= 200),
  body         bytea       NOT NULL CHECK (octet_length(body) <= 65536),
  PRIMARY KEY (store_id, received_at, receipt_id)
) PARTITION BY RANGE (received_at);

ALTER TABLE webhook_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_receipts
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE webhook_bodies ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_bodies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_bodies
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON webhook_bodies FROM app_user;

INSERT INTO app.monthly_partitioned (parent, key_column, append_only, retain_months) VALUES
  ('webhook_bodies', 'received_at', true, 3);
SELECT app.ensure_monthly_partitions(3, 'webhook_bodies');

-- +goose Down
DELETE FROM app.monthly_partitioned WHERE parent = 'webhook_bodies';
DROP TABLE webhook_bodies;
DROP TABLE webhook_receipts;
