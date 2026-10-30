-- Ending trials and cancelled periods (ADR-0050): the daily run's list of
-- stores whose plan stopped applying, as the table's owner through the
-- read-only billing_lookup policy (00058). It returns store IDs only.
--
-- Expand only: a function.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION app.periods_ended(p_now timestamptz)
  RETURNS SETOF uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT s.store_id FROM public.subscriptions s
  WHERE (s.status = 'trialing' AND s.trial_ends_at <= p_now)
     OR (s.status = 'active' AND s.cancel_at_period_end AND s.period_end <= p_now)
  ORDER BY 1
  LIMIT 10000
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.periods_ended(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.periods_ended(timestamptz) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.periods_ended(timestamptz);
