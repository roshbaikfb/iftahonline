-- The console's alerts (ADR-0080): a check every 5 minutes opens an alert
-- when a problem shows in two checks in a row and resolves it after two
-- clean ones; each admin whose role covers it is told once per opening
-- (and once per resolution of a red one) on each channel.

-- +goose Up
ALTER TABLE platform_admins ADD COLUMN IF NOT EXISTS alert_email boolean NOT NULL DEFAULT true;

-- One row per kind: the streaks of the latest checks.
CREATE TABLE admin_alert_state (
  kind       text        PRIMARY KEY CHECK (kind ~ '^[a-z_]{1,32}$'),
  bad        int         NOT NULL DEFAULT 0 CHECK (bad >= 0),
  good       int         NOT NULL DEFAULT 0 CHECK (good >= 0),
  checked_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE admin_alerts (
  id           uuid        PRIMARY KEY,
  kind         text        NOT NULL CHECK (kind ~ '^[a-z_]{1,32}$'),
  severity     text        NOT NULL CHECK (severity IN ('red', 'amber')),
  value        bigint      NOT NULL DEFAULT 0,
  opened_at    timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  resolved_at  timestamptz
);
-- At most one open alert per kind.
CREATE UNIQUE INDEX admin_alerts_open_kind ON admin_alerts (kind) WHERE resolved_at IS NULL;
CREATE INDEX admin_alerts_opened_idx ON admin_alerts (opened_at DESC);

-- What was sent to whom: once per alert, admin, event and channel.
CREATE TABLE admin_alert_deliveries (
  alert_id uuid        NOT NULL REFERENCES admin_alerts (id) ON DELETE CASCADE,
  admin_id uuid        NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE,
  event    text        NOT NULL CHECK (event IN ('opened', 'resolved')),
  channel  text        NOT NULL CHECK (channel IN ('email', 'push')),
  sent_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (alert_id, admin_id, event, channel)
);
CREATE INDEX admin_alert_deliveries_admin_idx ON admin_alert_deliveries (admin_id);

REVOKE ALL ON admin_alert_state, admin_alerts, admin_alert_deliveries FROM app_user;
GRANT SELECT, INSERT, UPDATE ON admin_alert_state, admin_alerts TO app_admin;
GRANT SELECT, INSERT ON admin_alert_deliveries TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS admin_alert_deliveries;
DROP TABLE IF EXISTS admin_alerts;
DROP TABLE IF EXISTS admin_alert_state;
ALTER TABLE platform_admins DROP COLUMN IF EXISTS alert_email;
