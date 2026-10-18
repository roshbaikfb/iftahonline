-- Create audit_log partitions without blocking writers (fixes a deadlock).
--
-- 00011 used CREATE TABLE ... PARTITION OF, which takes ACCESS EXCLUSIVE
-- on audit_log and then SHARE ROW EXCLUSIVE on stores (to clone the FK).
-- A concurrent sign-up holds ROW EXCLUSIVE on stores and waits for
-- audit_log: a lock cycle, i.e. a deadlock. It surfaced in CI under
-- parallel tests and would happen in production whenever the monthly
-- partition is created during traffic.
--
-- Now the partition is built as a standalone table (with its RLS,
-- grants and a CHECK matching its bounds) and then attached. ATTACH
-- PARTITION takes only SHARE UPDATE EXCLUSIVE on the parent, which does
-- not conflict with inserts, and the CHECK lets it skip the validation
-- scan. lock_timeout bounds any remaining wait; the job retries.

-- +goose Up
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.ensure_audit_log_partitions(months_ahead int) RETURNS int
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
