-- Order confirmation by message (ADR-0040): a cash-on-delivery order
-- asks its customer to confirm or cancel, by WhatsApp buttons or an SMS
-- link; no answer within the store's hours cancels it, if the message
-- reached the customer. The merchant is told in the dashboard (and by
-- email for quota warnings).
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE IF NOT EXISTS messaging_settings (
  store_id          uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  confirm_cod       boolean     NOT NULL DEFAULT true,
  auto_cancel_hours int         NOT NULL DEFAULT 24 CHECK (auto_cancel_hours BETWEEN 1 AND 72),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

-- One per order; the order's ID is the key (orders are per store).
CREATE TABLE IF NOT EXISTS order_confirmations (
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id    uuid        NOT NULL,
  message_id  uuid,
  status      text        NOT NULL
              CHECK (status IN ('pending', 'confirmed', 'cancelled', 'expired', 'skipped', 'unreached', 'stale')),
  -- SHA-256 of the SMS link's token (the token itself is only in the
  -- sealed message).
  token_hash  bytea       CHECK (octet_length(token_hash) = 32),
  expires_at  timestamptz NOT NULL,
  answered_at timestamptz,
  answered_by text        NOT NULL DEFAULT '' CHECK (char_length(answered_by) <= 64),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id, so it also covers the FK.
  PRIMARY KEY (store_id, order_id)
);
-- The SMS link's lookup (6c-2).
CREATE UNIQUE INDEX IF NOT EXISTS order_confirmations_token ON order_confirmations (store_id, token_hash) WHERE token_hash IS NOT NULL;

-- The merchant's notices. dedup_key makes each notice once: a quota
-- warning once per month and threshold, an order's notice once per order.
CREATE TABLE IF NOT EXISTS merchant_notifications (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  id         uuid        NOT NULL,
  kind       text        NOT NULL CHECK (kind ~ '^[a-z][a-z_]{0,39}$'),
  dedup_key  text        NOT NULL CHECK (char_length(dedup_key) BETWEEN 1 AND 80),
  data       jsonb       NOT NULL DEFAULT '{}' CHECK (octet_length(data::text) <= 2048),
  read_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, id),
  CONSTRAINT merchant_notifications_once UNIQUE (store_id, kind, dedup_key)
);

ALTER TABLE messaging_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE messaging_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON messaging_settings;
CREATE POLICY tenant_isolation ON messaging_settings
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
ALTER TABLE order_confirmations ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_confirmations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON order_confirmations;
CREATE POLICY tenant_isolation ON order_confirmations
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON order_confirmations FROM app_user;
ALTER TABLE merchant_notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_notifications FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON merchant_notifications;
CREATE POLICY tenant_isolation ON merchant_notifications
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS merchant_notifications;
DROP TABLE IF EXISTS order_confirmations;
DROP TABLE IF EXISTS messaging_settings;
