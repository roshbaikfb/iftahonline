-- One partition maintainer for every table partitioned by month.
--
-- audit_log had its own function (00011, 00013). Orders, order items and
-- order events (P4) are partitioned by month too, so the function is now
-- generic and driven by a registry: a migration that creates a monthly
-- partitioned table registers it here, and the platform job keeps every
-- registered table's partitions ahead of time.
--
-- The function stays SECURITY DEFINER and still takes no table names from
-- the caller: it only touches tables listed in the registry, which
-- app_user cannot write. Partitions are built standalone and attached
-- (see 00013 for why: no ACCESS EXCLUSIVE on the parent, no deadlock with
-- concurrent inserts).

-- +goose Up
CREATE TABLE app.monthly_partitioned (
  parent      text    PRIMARY KEY CHECK (parent ~ '^[a-z_]+$'),
  -- The RANGE partition key; partitions get a CHECK on it so ATTACH can
  -- skip the validation scan.
  key_column  text    NOT NULL CHECK (key_column ~ '^[a-z_]+$'),
  -- Append-only tables lose UPDATE and DELETE on their partitions too.
  append_only boolean NOT NULL
);
REVOKE ALL ON app.monthly_partitioned FROM PUBLIC;

INSERT INTO app.monthly_partitioned (parent, key_column, append_only) VALUES ('audit_log', 'occurred_at', true);

-- Creates the missing monthly partitions, from the current month to
-- months_ahead (at most 24) months later, of every registered table, or
-- of just `only` when it is set. Each partition gets its own RLS and
-- grants: querying a partition directly skips the parent's policies.
-- +goose StatementBegin
CREATE FUNCTION app.ensure_monthly_partitions(months_ahead int, only_parent text DEFAULT NULL) RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
  SET lock_timeout = '10s'
AS $$
DECLARE
  t       record;
  first   date := date_trunc('month', now())::date;
  last    date := (date_trunc('month', now()) + make_interval(months => least(greatest(months_ahead, 0), 24)))::date;
  m       date;
  nxt     date;
  name    text;
  created int := 0;
BEGIN
  FOR t IN SELECT parent, key_column, append_only FROM app.monthly_partitioned
           WHERE only_parent IS NULL OR parent = only_parent ORDER BY parent LOOP
    m := first;
    WHILE m <= last LOOP
      name := format('%s_%s', t.parent, to_char(m, 'YYYY_MM'));
      nxt := (m + interval '1 month')::date;
      IF to_regclass('public.' || name) IS NULL THEN
        EXECUTE format('CREATE TABLE public.%I (LIKE public.%I INCLUDING DEFAULTS INCLUDING CONSTRAINTS)', name, t.parent);
        EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I CHECK (%I >= %L AND %I < %L)',
                       name, name || '_bounds', t.key_column, m, t.key_column, nxt);
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', name);
        EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', name);
        EXECUTE format('CREATE POLICY tenant_isolation ON public.%I USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())', name);
        IF t.append_only THEN
          EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON public.%I FROM app_user', name);
        ELSE
          EXECUTE format('REVOKE TRUNCATE ON public.%I FROM app_user', name);
        END IF;
        EXECUTE format('ALTER TABLE public.%I ATTACH PARTITION public.%I FOR VALUES FROM (%L) TO (%L)', t.parent, name, m, nxt);
        created := created + 1;
      END IF;
      m := nxt;
    END LOOP;
  END LOOP;
  RETURN created;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.ensure_monthly_partitions(int, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.ensure_monthly_partitions(int, text) TO app_user;

-- The old entry point stays until nothing calls it (expand, then
-- contract).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.ensure_audit_log_partitions(months_ahead int) RETURNS int
  LANGUAGE sql SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$ SELECT app.ensure_monthly_partitions(months_ahead, 'audit_log') $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.ensure_audit_log_partitions(months_ahead int) RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
  SET lock_timeout = '10s'
AS $$
DECLARE
  m       date := date_trunc('month', now())::date;
  last    date := (date_trunc('month', now()) + make_interval(months => least(greatest(months_ahead, 0), 24)))::date;
  nxt     date;
  name    text;
  created int := 0;
BEGIN
  WHILE m <= last LOOP
    name := format('audit_log_%s', to_char(m, 'YYYY_MM'));
    nxt := (m + interval '1 month')::date;
    IF to_regclass('public.' || name) IS NULL THEN
      EXECUTE format('CREATE TABLE public.%I (LIKE public.audit_log INCLUDING DEFAULTS INCLUDING CONSTRAINTS)', name);
      EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I CHECK (occurred_at >= %L AND occurred_at < %L)',
                     name, name || '_bounds', m, nxt);
      EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', name);
      EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', name);
      EXECUTE format('CREATE POLICY tenant_isolation ON public.%I USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id())', name);
      EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON public.%I FROM app_user', name);
      EXECUTE format('ALTER TABLE public.audit_log ATTACH PARTITION public.%I FOR VALUES FROM (%L) TO (%L)', name, m, nxt);
      created := created + 1;
    END IF;
    m := nxt;
  END LOOP;
  RETURN created;
END
$$;
-- +goose StatementEnd
DROP FUNCTION app.ensure_monthly_partitions(int, text);
DROP TABLE app.monthly_partitioned;
