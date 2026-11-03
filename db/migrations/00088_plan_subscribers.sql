-- How many stores are on each plan version, for the console's plans
-- screen (ADR-0058's rule: counts, never rows).

-- +goose Up
-- app.plan_subscribers counts the subscriptions on each plan version
-- that are trialing, active or past due: the stores that renew into the
-- plan's next version. Suspended and cancelled ones are left out.
-- +goose StatementBegin
CREATE FUNCTION app.plan_subscribers()
  RETURNS TABLE (plan_id uuid, subscribers bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT s.plan_id, count(*)
  FROM public.subscriptions s
  WHERE s.status IN ('trialing', 'active', 'past_due')
  GROUP BY s.plan_id
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.plan_subscribers() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.plan_subscribers() TO app_admin;

-- +goose Down
DROP FUNCTION IF EXISTS app.plan_subscribers();
