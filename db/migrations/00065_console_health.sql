-- The console's health page (ADR-0058): counts, never rows.
--
-- app.billing_health() counts subscriptions by status and open and
-- overdue invoices across every store and account, as the table owner
-- through the read-only billing_lookup policies (00058, 00060), for the
-- billing module (app_user). app.job_health() counts River's jobs that
-- wait, retry or were discarded lately, for the console (app_admin),
-- which reads no job itself. Both return numbers only.
--
-- Expand only: two functions.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION app.billing_health(p_now timestamptz)
  RETURNS TABLE (trialing bigint, active bigint, past_due bigint, suspended bigint, cancelled bigint,
                 invoices_open bigint, invoices_overdue bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT
    count(*) FILTER (WHERE s.status = 'trialing'),
    count(*) FILTER (WHERE s.status = 'active'),
    count(*) FILTER (WHERE s.status = 'past_due'),
    count(*) FILTER (WHERE s.status = 'suspended'),
    count(*) FILTER (WHERE s.status = 'cancelled'),
    (SELECT count(*) FROM public.invoices i WHERE i.status = 'open'),
    (SELECT count(*) FROM public.invoices i WHERE i.status = 'open' AND i.due_at < p_now)
  FROM public.subscriptions s
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_health(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_health(timestamptz) TO app_user;

-- +goose StatementBegin
CREATE FUNCTION app.job_health(p_now timestamptz)
  RETURNS TABLE (available bigint, running bigint, retryable bigint, discarded_24h bigint, oldest_available_seconds bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT
    count(*) FILTER (WHERE j.state = 'available'),
    count(*) FILTER (WHERE j.state = 'running'),
    count(*) FILTER (WHERE j.state = 'retryable'),
    (SELECT count(*) FROM public.river_job d WHERE d.state = 'discarded' AND d.finalized_at > p_now - interval '24 hours'),
    coalesce(extract(epoch FROM p_now - min(j.scheduled_at) FILTER (WHERE j.state = 'available' AND j.scheduled_at <= p_now))::bigint, 0)
  FROM public.river_job j
  WHERE j.state IN ('available', 'running', 'retryable')
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.job_health(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.job_health(timestamptz) TO app_admin;

-- +goose Down
DROP FUNCTION IF EXISTS app.job_health(timestamptz);
DROP FUNCTION IF EXISTS app.billing_health(timestamptz);
