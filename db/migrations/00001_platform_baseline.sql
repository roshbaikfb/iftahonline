-- Platform baseline: extensions, the RLS helper and default grants.
--
-- Roles (created by infrastructure, not by migrations; see
-- deploy/compose/initdb/01-roles.sql and ADR-0002):
--   app_owner  owns all objects; migrations run as this role.
--   app_user   used by api/storefront/worker; NOBYPASSRLS, DML only.
--
-- RLS pattern for every tenant-owned table (added by later migrations):
--   ALTER TABLE t ENABLE ROW LEVEL SECURITY;
--   ALTER TABLE t FORCE ROW LEVEL SECURITY;
--   CREATE POLICY tenant_isolation ON t
--     USING (store_id = app.current_store_id())
--     WITH CHECK (store_id = app.current_store_id());

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
    RAISE EXCEPTION 'role app_user must exist before migrating (see deploy/compose/initdb/01-roles.sql)';
  END IF;
  IF (SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = 'app_user') THEN
    RAISE EXCEPTION 'role app_user must be NOSUPERUSER NOBYPASSRLS (ADR-0002)';
  END IF;
END
$$;
-- +goose StatementEnd

CREATE EXTENSION IF NOT EXISTS citext;

CREATE SCHEMA app;
GRANT USAGE ON SCHEMA app TO app_user;

-- The current tenant for row level security. NULL (matching no rows) when
-- postgres.InTenantTx has not set app.store_id in this transaction.
-- +goose StatementBegin
CREATE FUNCTION app.current_store_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
AS $$
  SELECT nullif(current_setting('app.store_id', true), '')::uuid
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO app_user;

-- +goose Down
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  REVOKE USAGE, SELECT ON SEQUENCES FROM app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM app_user;
REVOKE USAGE ON SCHEMA public FROM app_user;
DROP SCHEMA app CASCADE;
DROP EXTENSION IF EXISTS citext;
