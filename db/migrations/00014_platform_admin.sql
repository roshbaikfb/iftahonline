-- Super-admin console (ADR-0005 §8, ADR-0007 §5).
--
-- Platform admins are not users: separate table, separate sessions,
-- mandatory TOTP, their own append-only audit log. The merchant role
-- (app_user) has no access at all to these tables; the admin role
-- (app_admin, created by infrastructure) gets only what the console
-- needs: its own tables, and a read-only view of the store directory
-- across tenants through explicit policies. Changes to a store (suspend,
-- reactivate) still go through the tenancy module as app_user inside
-- that store's tenant transaction, so RLS, cache invalidation and the
-- store's own audit log apply unchanged.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_admin') THEN
    RAISE EXCEPTION 'role app_admin must exist before migrating (see deploy/compose/initdb/01-roles.sql)';
  END IF;
  IF (SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = 'app_admin') THEN
    RAISE EXCEPTION 'role app_admin must be NOSUPERUSER NOBYPASSRLS (ADR-0007)';
  END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO app_admin;
GRANT USAGE ON SCHEMA app TO app_admin;

CREATE TABLE platform_admins (
  id             uuid        PRIMARY KEY,
  email          citext      NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
  name           text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  password_hash  text        NOT NULL,
  totp_secret    bytea       NOT NULL, -- sealed (AAD "admin-totp:<id>"); 2FA is mandatory
  totp_last_step bigint      NOT NULL DEFAULT 0,
  status         text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  last_login_at  timestamptz,
  CONSTRAINT platform_admins_email_key UNIQUE (email)
);

CREATE TABLE admin_sessions (
  id           uuid        PRIMARY KEY,
  admin_id     uuid        NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE,
  token_hash   bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  user_agent   text        NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 200),
  ip_prefix    text        NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,
  CONSTRAINT admin_sessions_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX admin_sessions_admin_id_idx ON admin_sessions (admin_id);

CREATE TABLE admin_audit_log (
  id          uuid        PRIMARY KEY,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  admin_id    uuid        REFERENCES platform_admins (id),
  action      text        NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
  target_type text        NOT NULL DEFAULT '',
  target_id   text        NOT NULL DEFAULT '',
  ip_prefix   text        NOT NULL DEFAULT '',
  metadata    jsonb       NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX admin_audit_log_occurred_idx ON admin_audit_log (occurred_at DESC, id DESC);
CREATE INDEX admin_audit_log_admin_id_idx ON admin_audit_log (admin_id);

-- +goose StatementBegin
CREATE FUNCTION app.reject_change() RETURNS trigger
  LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER admin_audit_log_append_only BEFORE UPDATE OR DELETE ON admin_audit_log
  FOR EACH ROW EXECUTE FUNCTION app.reject_change();

-- Default privileges gave app_user DML on every new table: take it back.
REVOKE ALL ON platform_admins, admin_sessions, admin_audit_log FROM app_user;
GRANT SELECT, INSERT, UPDATE ON platform_admins, admin_sessions TO app_admin;
GRANT SELECT, INSERT ON admin_audit_log TO app_admin;

-- The read-only cross-tenant store directory for the console.
GRANT SELECT ON stores, store_domains TO app_admin;
CREATE POLICY admin_read ON stores FOR SELECT TO app_admin USING (true);
CREATE POLICY admin_read ON store_domains FOR SELECT TO app_admin USING (true);

-- +goose Down
DROP POLICY admin_read ON store_domains;
DROP POLICY admin_read ON stores;
REVOKE SELECT ON stores, store_domains FROM app_admin;
DROP TABLE admin_audit_log;
DROP FUNCTION app.reject_change();
DROP TABLE admin_sessions;
DROP TABLE platform_admins;
REVOKE USAGE ON SCHEMA app FROM app_admin;
REVOKE USAGE ON SCHEMA public FROM app_admin;
