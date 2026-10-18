-- Identity: users, sessions, store memberships, and the append-only audit
-- log (ADR-0005, ADR-0006, ADR-0007).
--
-- users and user_sessions are platform-wide (a user may belong to many
-- stores), so they have no store_id and no tenant RLS; only the identity
-- module touches them. store_memberships and audit_log are tenant tables.

-- +goose Up

-- The current user for user-scoped RLS reads. NULL (matching nothing)
-- unless set with SET LOCAL semantics, exactly like app.current_store_id.
-- +goose StatementBegin
CREATE FUNCTION app.current_user_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
AS $$
  SELECT nullif(current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd

CREATE TABLE users (
  id                uuid        PRIMARY KEY,
  email             citext      NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
  email_verified_at timestamptz,
  name              text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  password_hash     text        NOT NULL,
  status            text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT users_email_key UNIQUE (email)
);

-- Only the SHA-256 of a session token is stored; the token itself exists
-- only in the client's cookie or app storage.
CREATE TABLE user_sessions (
  id           uuid        PRIMARY KEY,
  user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash   bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  user_agent   text        NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 200),
  ip_prefix    text        NOT NULL DEFAULT '', -- /24 (IPv4) or /48 (IPv6); never the full address
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,
  CONSTRAINT user_sessions_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX user_sessions_user_id_idx ON user_sessions (user_id, created_at DESC);

CREATE TABLE store_memberships (
  store_id   uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  role       text        NOT NULL CHECK (role IN ('owner', 'manager', 'support', 'shipping')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, user_id)
);
CREATE INDEX store_memberships_user_id_idx ON store_memberships (user_id);
CREATE UNIQUE INDEX store_memberships_one_owner ON store_memberships (store_id) WHERE role = 'owner';

ALTER TABLE store_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_memberships
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
-- A user may read their own memberships across stores ("my stores");
-- read-only, and only their own rows (ADR-0007 §4).
CREATE POLICY member_self_read ON store_memberships FOR SELECT
  USING (user_id = app.current_user_id());

-- Append-only, month-partitioned audit trail of store actions.
CREATE TABLE audit_log (
  id            uuid        NOT NULL,
  store_id      uuid        NOT NULL REFERENCES stores (id),
  occurred_at   timestamptz NOT NULL DEFAULT now(),
  actor_user_id uuid,
  action        text        NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
  target_type   text        NOT NULL DEFAULT '',
  target_id     text        NOT NULL DEFAULT '',
  ip_prefix     text        NOT NULL DEFAULT '',
  metadata      jsonb       NOT NULL DEFAULT '{}'::jsonb,
  PRIMARY KEY (store_id, occurred_at, id)
) PARTITION BY RANGE (occurred_at);

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_log
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM app_user;

-- +goose StatementBegin
CREATE FUNCTION app.audit_log_is_append_only() RETURNS trigger
  LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
-- Row triggers on a partitioned table are cloned onto every partition.
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION app.audit_log_is_append_only();

-- Creates the monthly partitions of audit_log from the current month to
-- months_ahead months later. Each partition gets its own RLS and grants:
-- querying a partition directly skips the parent's policies. SECURITY
-- DEFINER so the worker (app_user) can run it; it takes no table names
-- from the caller.
-- +goose StatementBegin
CREATE FUNCTION app.ensure_audit_log_partitions(months_ahead int) RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
DECLARE
  m       date := date_trunc('month', now())::date;
  last    date := (date_trunc('month', now()) + make_interval(months => least(greatest(months_ahead, 0), 24)))::date;
  name    text;
  created int := 0;
BEGIN
  WHILE m <= last LOOP
    name := format('audit_log_%s', to_char(m, 'YYYY_MM'));
    IF to_regclass('public.' || name) IS NULL THEN
      EXECUTE format('CREATE TABLE public.%I PARTITION OF public.audit_log FOR VALUES FROM (%L) TO (%L)',
                     name, m, (m + interval '1 month')::date);
      EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', name);
      EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', name);
      EXECUTE format('CREATE POLICY tenant_isolation ON public.%I USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())', name);
      EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON public.%I FROM app_user', name);
      created := created + 1;
    END IF;
    m := (m + interval '1 month')::date;
  END LOOP;
  RETURN created;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.ensure_audit_log_partitions(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.ensure_audit_log_partitions(int) TO app_user;

SELECT app.ensure_audit_log_partitions(2);

-- +goose Down
DROP TABLE audit_log;
DROP FUNCTION app.ensure_audit_log_partitions(int);
DROP FUNCTION app.audit_log_is_append_only();
DROP TABLE store_memberships;
DROP TABLE user_sessions;
DROP TABLE users;
DROP FUNCTION app.current_user_id();
