-- Customer messaging (ADR-0039): the log of every message sent on a
-- store's behalf over WhatsApp or SMS.
--
-- A message is queued in the transaction that asks for it, with its
-- quota unit (entitlements) and its send job. The recipient and the
-- values its texts fill in are sealed in `content` (secretbox, AAD
-- messaging:<store>:<message>); the log shows the number masked. Statuses
-- come back by webhook and only move forward.
--
-- Partitioned by month on the message's ULID time, so a webhook that
-- names a message (our reference) reads one partition. Partitions are
-- dropped after 6 whole months.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE IF NOT EXISTS messages (
  store_id            uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  created_at          timestamptz NOT NULL,
  id                  uuid        NOT NULL,
  kind                text        NOT NULL CHECK (kind ~ '^[a-z][a-z_]{0,39}$'),
  locale              text        NOT NULL CHECK (locale IN ('ar', 'en')),
  order_id            uuid,
  status              text        NOT NULL
                      CHECK (status IN ('queued', 'sent', 'delivered', 'read', 'failed', 'skipped_quota')),
  channel             text        NOT NULL DEFAULT '' CHECK (channel IN ('', 'whatsapp', 'sms')),
  provider            text        NOT NULL DEFAULT '' CHECK (provider ~ '^([a-z][a-z0-9_]{1,31})?$'),
  provider_message_id text        NOT NULL DEFAULT '' CHECK (char_length(provider_message_id) <= 200),
  to_masked           text        NOT NULL CHECK (char_length(to_masked) <= 32),
  content             bytea       CHECK (octet_length(content) <= 4096),
  quota_period        date,
  fallback            boolean     NOT NULL DEFAULT false,
  error_code          text        NOT NULL DEFAULT '' CHECK (char_length(error_code) <= 40),
  reply               text        NOT NULL DEFAULT '' CHECK (reply IN ('', 'confirm', 'cancel')),
  replied_at          timestamptz,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, created_at, id)
) PARTITION BY RANGE (created_at);
-- An order's messages (6c); the PK serves the log, newest first.
CREATE INDEX IF NOT EXISTS messages_store_order ON messages (store_id, order_id, created_at) WHERE order_id IS NOT NULL;

ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON messages;
CREATE POLICY tenant_isolation ON messages
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON messages FROM app_user;

INSERT INTO app.monthly_partitioned (parent, key_column, append_only, retain_months) VALUES
  ('messages', 'created_at', false, 6)
ON CONFLICT DO NOTHING;
SELECT app.ensure_monthly_partitions(3, 'messages');

-- +goose Down
DELETE FROM app.monthly_partitioned WHERE parent = 'messages';
DROP TABLE IF EXISTS messages;
