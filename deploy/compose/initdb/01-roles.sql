-- Local/test database bootstrap (ADR-0002). Runs once, as the postgres
-- superuser, from /docker-entrypoint-initdb.d. Production creates the same
-- roles with managed secrets instead of these development passwords.
CREATE ROLE app_owner LOGIN PASSWORD 'app_owner'
  NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE app_user LOGIN PASSWORD 'app_user'
  NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
-- Super-admin console (ADR-0007 §5): reads the store directory across
-- tenants through explicit policies; never used by merchant traffic.
CREATE ROLE app_admin LOGIN PASSWORD 'app_admin'
  NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;

CREATE DATABASE iftah OWNER app_owner;
\connect iftah
ALTER SCHEMA public OWNER TO app_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
