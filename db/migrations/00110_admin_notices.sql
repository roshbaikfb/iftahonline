-- An admin's own account page (C3, the approved mockup): when an admin
-- changes their password or moves two-step verification to a new phone,
-- they are emailed, so a change they did not make is seen. The console's
-- role cannot insert River jobs, so the notice is an outbox row written in
-- the change's transaction and the worker mails it (admin.notices).
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE admin_notices (
  id         uuid        PRIMARY KEY,
  admin_id   uuid        NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE,
  kind       text        NOT NULL CHECK (kind IN ('password_changed', 'totp_changed')),
  ip_prefix  text        NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at    timestamptz
);
CREATE INDEX admin_notices_admin_id_idx ON admin_notices (admin_id);
CREATE INDEX admin_notices_unsent_idx ON admin_notices (created_at) WHERE sent_at IS NULL;
REVOKE ALL ON admin_notices FROM app_user;
GRANT SELECT, INSERT, UPDATE ON admin_notices TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS admin_notices;
