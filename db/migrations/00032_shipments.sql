-- Shipments: an order handed to a carrier (ADR-0027).
--
-- A shipment is requested in the transaction that writes it (its create
-- job is enqueued there), created when the carrier answers with a
-- tracking number, and then follows the carrier's states, from webhooks
-- (re-queried before use) and from a scheduled poll of each active
-- shipment. Its order moves with it (shipped, delivered, failed_delivery,
-- returned) in the same transaction.
--
-- An order has at most one live shipment (not cancelled, not failed); a
-- tracking number is unique per store and carrier, which is how webhooks
-- find their shipment. shipment_events is the timeline, append-only and
-- partitioned by month.

-- +goose Up
CREATE TABLE shipments (
  id                  uuid        PRIMARY KEY,
  store_id            uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id            uuid        NOT NULL,
  connection_id       uuid        NOT NULL,
  carrier             text        NOT NULL CHECK (carrier ~ '^[a-z][a-z0-9_]{1,31}$'),
  status              text        NOT NULL DEFAULT 'requested' CHECK (status IN
                        ('requested', 'created', 'in_transit', 'out_for_delivery', 'delivered', 'failed_attempt',
                         'returned', 'cancelled', 'failed')),
  tracking_number     text        CHECK (char_length(tracking_number) BETWEEN 1 AND 64),
  provider_id         text        NOT NULL DEFAULT '' CHECK (char_length(provider_id) <= 64),
  currency            text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  cod_minor           bigint      NOT NULL CHECK (cod_minor >= 0),
  label_key           text        NOT NULL DEFAULT '' CHECK (char_length(label_key) <= 300),
  raw_state           text        NOT NULL DEFAULT '' CHECK (char_length(raw_state) <= 320),
  failure             text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 300),
  cancel_requested_at timestamptz,
  status_at           timestamptz NOT NULL DEFAULT now(),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  version             integer     NOT NULL DEFAULT 1 CHECK (version > 0),
  CONSTRAINT shipments_store_id_key UNIQUE (store_id, id),
  CONSTRAINT shipments_connection_fkey FOREIGN KEY (store_id, connection_id)
    REFERENCES carrier_connections (store_id, id)
);
-- One live shipment per order.
CREATE UNIQUE INDEX shipments_one_live_per_order ON shipments (store_id, order_id) WHERE status NOT IN ('cancelled', 'failed');
-- How a webhook finds its shipment.
CREATE UNIQUE INDEX shipments_tracking ON shipments (store_id, carrier, tracking_number) WHERE tracking_number IS NOT NULL;
-- The list, newest first; and by order (also covers the FK, store first).
CREATE INDEX shipments_store_list ON shipments (store_id, created_at DESC, id DESC);
CREATE INDEX shipments_store_order ON shipments (store_id, order_id, created_at DESC);
CREATE INDEX shipments_store_connection ON shipments (store_id, connection_id);

CREATE TABLE shipment_events (
  id          uuid        NOT NULL,
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  shipment_id uuid        NOT NULL,
  occurred_at timestamptz NOT NULL,
  status      text        NOT NULL,
  raw_state   text        NOT NULL DEFAULT '' CHECK (char_length(raw_state) <= 320),
  source      text        NOT NULL CHECK (source IN ('api', 'webhook', 'poll', 'merchant')),
  note        text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 300),
  PRIMARY KEY (store_id, occurred_at, id)
) PARTITION BY RANGE (occurred_at);
CREATE INDEX shipment_events_store_shipment ON shipment_events (store_id, shipment_id, occurred_at);

ALTER TABLE shipments ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipments
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE shipment_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipment_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shipment_events
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON shipment_events FROM app_user;

INSERT INTO app.monthly_partitioned (parent, key_column, append_only) VALUES ('shipment_events', 'occurred_at', true);
SELECT app.ensure_monthly_partitions(3, 'shipment_events');

-- +goose Down
DELETE FROM app.monthly_partitioned WHERE parent = 'shipment_events';
DROP TABLE shipment_events;
DROP TABLE shipments;
