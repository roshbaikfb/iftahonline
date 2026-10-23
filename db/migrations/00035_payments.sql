-- Online payments (ADR-0029).
--
-- A store connects its account at a gateway (Paymob); its keys are
-- envelope-sealed (ADR-0024). A card order gets a payment each time the
-- shopper is sent to the gateway's hosted page; the gateway's word,
-- re-queried after every webhook, moves it to paid, failed or expired,
-- and refunds move it on. No card data is ever stored: the shopper pays
-- on the gateway's page.

-- +goose Up
CREATE TABLE payment_connections (
  id              uuid        PRIMARY KEY,
  store_id        uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  gateway         text        NOT NULL CHECK (gateway ~ '^[a-z][a-z0-9_]{1,31}$'),
  credentials     bytea       NOT NULL CHECK (octet_length(credentials) BETWEEN 1 AND 8192),
  status          text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disconnected')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  disconnected_at timestamptz,
  CONSTRAINT payment_connections_store_id_key UNIQUE (store_id, id)
);
CREATE UNIQUE INDEX payment_connections_one_active ON payment_connections (store_id, gateway) WHERE status = 'active';
CREATE INDEX payment_connections_store_list ON payment_connections (store_id, created_at DESC);

CREATE TABLE payments (
  id             uuid        PRIMARY KEY,
  store_id       uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id       uuid        NOT NULL,
  connection_id  uuid        NOT NULL,
  gateway        text        NOT NULL CHECK (gateway ~ '^[a-z][a-z0-9_]{1,31}$'),
  currency       text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  amount_minor   bigint      NOT NULL CHECK (amount_minor > 0),
  status         text        NOT NULL DEFAULT 'pending' CHECK (status IN
                   ('pending', 'paid', 'failed', 'expired', 'partially_refunded', 'refunded')),
  refunded_minor bigint      NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0 AND refunded_minor <= amount_minor),
  checkout_url   text        NOT NULL DEFAULT '' CHECK (char_length(checkout_url) <= 2000),
  provider_ref   text        NOT NULL DEFAULT '' CHECK (char_length(provider_ref) <= 100),
  transaction_id text        NOT NULL DEFAULT '' CHECK (char_length(transaction_id) <= 100),
  failure        text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 300),
  status_at      timestamptz NOT NULL DEFAULT now(),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  version        integer     NOT NULL DEFAULT 1 CHECK (version > 0),
  CONSTRAINT payments_store_id_key UNIQUE (store_id, id),
  CONSTRAINT payments_connection_fkey FOREIGN KEY (store_id, connection_id) REFERENCES payment_connections (store_id, id)
);
CREATE INDEX payments_store_order ON payments (store_id, order_id, created_at DESC);
CREATE INDEX payments_store_list ON payments (store_id, created_at DESC, id DESC);
CREATE INDEX payments_store_connection ON payments (store_id, connection_id);

CREATE TABLE payment_refunds (
  id                 uuid        PRIMARY KEY,
  store_id           uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  payment_id         uuid        NOT NULL,
  currency           text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  amount_minor       bigint      NOT NULL CHECK (amount_minor > 0),
  status             text        NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'succeeded', 'failed')),
  reason             text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 300),
  provider_refund_id text        NOT NULL DEFAULT '' CHECK (char_length(provider_refund_id) <= 100),
  failure            text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 300),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT payment_refunds_payment_fkey FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, id) ON DELETE CASCADE
);
CREATE INDEX payment_refunds_store_payment ON payment_refunds (store_id, payment_id, created_at);

ALTER TABLE payment_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_connections
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payments
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE payment_refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_refunds FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_refunds
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE payment_refunds;
DROP TABLE payments;
DROP TABLE payment_connections;
