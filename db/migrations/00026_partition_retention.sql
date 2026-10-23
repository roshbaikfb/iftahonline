-- Retention for monthly partitioned tables (ADR-0024).
--
-- Raw inbound webhook bodies (P5) are kept about 90 days; dropping whole
-- monthly partitions is how a large table forgets old rows without
-- DELETE scans or bloat. A registered table with retain_months set keeps
-- at least that many whole months before the current one; older
-- partitions are detached and dropped by the platform partition job.
--
-- Like ensure_monthly_partitions, the function is SECURITY DEFINER,
-- takes no names from the caller and touches only registered tables and
-- their own <parent>_YYYY_MM partitions. DETACH waits for the parent's
-- lock at most lock_timeout; a busy day is retried the next day.

-- +goose Up
ALTER TABLE app.monthly_partitioned ADD COLUMN retain_months int CHECK (retain_months BETWEEN 1 AND 120);

-- +goose StatementBegin
CREATE FUNCTION app.drop_expired_partitions() RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
  SET lock_timeout = '5s'
AS $$
DECLARE
  t       record;
  p       record;
  m       date;
  dropped int := 0;
BEGIN
  FOR t IN SELECT parent, retain_months FROM app.monthly_partitioned
           WHERE retain_months IS NOT NULL ORDER BY parent LOOP
    FOR p IN SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
             WHERE i.inhparent = to_regclass('public.' || t.parent)
               AND c.relname ~ ('^' || t.parent || '_[0-9]{4}_[0-9]{2}$')
             ORDER BY c.relname LOOP
      m := to_date(right(p.relname, 7), 'YYYY_MM');
      -- The partition's month ended retain_months whole months ago.
      IF m + make_interval(months => t.retain_months + 1) <= date_trunc('month', now())::date THEN
        EXECUTE format('ALTER TABLE public.%I DETACH PARTITION public.%I', t.parent, p.relname);
        EXECUTE format('DROP TABLE public.%I', p.relname);
        dropped := dropped + 1;
      END IF;
    END LOOP;
  END LOOP;
  RETURN dropped;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.drop_expired_partitions() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.drop_expired_partitions() TO app_user;

-- +goose Down
DROP FUNCTION app.drop_expired_partitions();
ALTER TABLE app.monthly_partitioned DROP COLUMN retain_months;
