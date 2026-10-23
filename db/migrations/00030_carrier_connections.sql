-- A store's accounts at carriers (ADR-0026).
--
-- The merchant's API key is sealed with envelope encryption (ADR-0024),
-- its AAD naming the carrier, store and connection, so a blob copied to
-- another row does not open. The webhook secret we generate is shown to
-- the merchant once; only its SHA-256 is kept, compared in constant time
-- when a delivery arrives. A store has at most one active connection per
-- carrier; disconnected ones are kept for the shipments that used them.

-- +goose Up
CREATE TABLE carrier_connections (
  id                  uuid        PRIMARY KEY,
  store_id            uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  carrier             text        NOT NULL CHECK (carrier ~ '^[a-z][a-z0-9_]{1,31}$'),
  credentials         bytea       NOT NULL CHECK (octet_length(credentials) BETWEEN 1 AND 4096),
  webhook_secret_hash bytea       NOT NULL CHECK (octet_length(webhook_secret_hash) = 32),
  status              text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disconnected')),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  disconnected_at     timestamptz,
  CONSTRAINT carrier_connections_store_id_key UNIQUE (store_id, id)
);
CREATE UNIQUE INDEX carrier_connections_one_active ON carrier_connections (store_id, carrier) WHERE status = 'active';
-- Leads with store_id (covers the FK); the store's connections, newest first.
CREATE INDEX carrier_connections_store_list ON carrier_connections (store_id, created_at DESC);

ALTER TABLE carrier_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE carrier_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON carrier_connections
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE carrier_connections;
