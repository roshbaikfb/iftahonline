-- A store's own texts for its customer messages (ADR-0039): the SMS
-- body per message kind and locale. WhatsApp sends the platform's
-- templates Meta approved, so only the SMS text is the store's. No row
-- means the default text.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE IF NOT EXISTS message_templates (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  kind       text        NOT NULL CHECK (kind ~ '^[a-z][a-z_]{0,39}$'),
  locale     text        NOT NULL CHECK (locale IN ('ar', 'en')),
  sms_body   text        NOT NULL CHECK (char_length(sms_body) BETWEEN 1 AND 400),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id, so it also covers the FK.
  PRIMARY KEY (store_id, kind, locale)
);

ALTER TABLE message_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_templates FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON message_templates;
CREATE POLICY tenant_isolation ON message_templates
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE IF EXISTS message_templates;
