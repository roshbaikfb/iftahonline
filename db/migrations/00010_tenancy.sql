-- Tenancy: the store directory and its domains (ADR-0002, ADR-0004).
--
-- stores is the tenant root: its own id is the tenant key, so its policy
-- compares id (not store_id) with app.current_store_id(). store_domains is
-- a regular tenant table.
--
-- Host lookups cross tenants by nature (the tenant is what we are looking
-- for), so they go through app.resolve_host, a SECURITY DEFINER function
-- owned by app_owner that returns only (store_id, store_status). FORCE RLS
-- applies to the owner too, so app_owner gets a read-only directory policy
-- on both tables; app_owner owns them and could disable RLS anyway.

-- +goose Up
CREATE TABLE stores (
  id         uuid        PRIMARY KEY,
  slug       citext      NOT NULL,
  name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  status     text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
  plan       text        NOT NULL DEFAULT 'free' CHECK (plan IN ('free', 'basic', 'pro')),
  currency   char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  locale     text        NOT NULL CHECK (locale IN ('ar', 'en')),
  timezone   text        NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT stores_slug_key UNIQUE (slug),
  CONSTRAINT stores_slug_format CHECK (slug::text ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])$')
);

ALTER TABLE stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE stores FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stores
  USING (id = app.current_store_id())
  WITH CHECK (id = app.current_store_id());
CREATE POLICY directory_lookup ON stores FOR SELECT TO app_owner USING (true);

CREATE TABLE store_domains (
  id                 uuid        PRIMARY KEY,
  store_id           uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  host               citext      NOT NULL CHECK (char_length(host) BETWEEN 3 AND 253),
  kind               text        NOT NULL CHECK (kind IN ('subdomain', 'custom')),
  status             text        NOT NULL CHECK (status IN ('pending', 'verified', 'failed')),
  is_primary         boolean     NOT NULL DEFAULT false,
  verification_token text        NOT NULL DEFAULT '',
  failure_reason     text        NOT NULL DEFAULT '',
  check_attempts     integer     NOT NULL DEFAULT 0,
  verified_at        timestamptz,
  last_checked_at    timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT store_domains_store_host_key UNIQUE (store_id, host),
  CONSTRAINT store_domains_primary_is_verified CHECK (NOT is_primary OR status = 'verified')
);

-- A host routes to at most one store. Pending claims may coexist, so
-- nobody can squat a domain they do not control; the first store to
-- prove ownership wins.
CREATE UNIQUE INDEX store_domains_verified_host_key ON store_domains (host) WHERE status = 'verified';
CREATE UNIQUE INDEX store_domains_one_primary ON store_domains (store_id) WHERE is_primary;
-- store_domains_store_host_key leads with store_id and covers the FK.

ALTER TABLE store_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_domains
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
CREATE POLICY directory_lookup ON store_domains FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.resolve_host(p_host text)
  RETURNS TABLE (store_id uuid, store_status text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT d.store_id, s.status
  FROM public.store_domains d
  JOIN public.stores s ON s.id = d.store_id
  WHERE d.host = p_host::public.citext AND d.status = 'verified'
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.resolve_host(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.resolve_host(text) TO app_user;

-- +goose Down
DROP FUNCTION app.resolve_host(text);
DROP TABLE store_domains;
DROP TABLE stores;
