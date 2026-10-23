-- Cash-on-delivery settlements: what carriers paid out (ADR-0028).
--
-- A row is one payout line for one parcel: from the carrier's API where
-- it has one, or from the statement a merchant downloads from the
-- carrier's dashboard and uploads (Bosta has no settlements API). Lines
-- are matched to shipments by tracking number within the store and
-- carrier; an unmatched line is kept (shipment_id null) for the
-- discrepancies report. Importing a statement twice adds nothing: a line
-- is unique per connection, tracking number and payout reference.
--
-- The report compares, per carrier, what delivered parcels should have
-- brought in (shipments.cod_minor) with what was settled, and ages the
-- difference: money still held by the carrier.

-- +goose Up
CREATE TABLE cod_settlements (
  id              uuid        PRIMARY KEY,
  store_id        uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  connection_id   uuid        NOT NULL,
  carrier         text        NOT NULL CHECK (carrier ~ '^[a-z][a-z0-9_]{1,31}$'),
  tracking_number text        NOT NULL CHECK (char_length(tracking_number) BETWEEN 1 AND 64),
  shipment_id     uuid,
  currency        text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  amount_minor    bigint      NOT NULL CHECK (amount_minor BETWEEN -1000000000 AND 1000000000),
  settled_at      timestamptz NOT NULL,
  reference       text        NOT NULL DEFAULT '' CHECK (char_length(reference) <= 100),
  source          text        NOT NULL CHECK (source IN ('api', 'import')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT cod_settlements_line_key UNIQUE (store_id, connection_id, tracking_number, reference),
  CONSTRAINT cod_settlements_connection_fkey FOREIGN KEY (store_id, connection_id)
    REFERENCES carrier_connections (store_id, id)
);
-- Settled per shipment; the unmatched lines.
CREATE INDEX cod_settlements_store_shipment ON cod_settlements (store_id, shipment_id);
CREATE INDEX cod_settlements_store_unmatched ON cod_settlements (store_id, settled_at DESC) WHERE shipment_id IS NULL;
-- Delivered parcels by delivery time, for the report and the held list.
CREATE INDEX shipments_store_status_at ON shipments (store_id, status, status_at, id);

ALTER TABLE cod_settlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE cod_settlements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cod_settlements
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP INDEX shipments_store_status_at;
DROP TABLE cod_settlements;
