-- Agencies set up from the console (ADR-0059).
--
-- An agency account names the plan its stores are on (plan_code: every
-- version of that code, as stores renew). A store joins by moving its
-- membership row from its own account to the agency's (approved
-- 2026-10-31: allowed when its own account has no open invoice; its old
-- invoices stay there), so app_user may now delete a membership row; the
-- invoices themselves stay append-only. app.agency_accounts() lists the
-- agencies for the console, as the table owner through read-only
-- policies, like app.billable_stores: names and counts, never members'
-- data.
--
-- Expand only: a nullable column, a grant, policies and a function.

-- +goose Up
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS plan_code text;
ALTER TABLE billing_accounts ADD CONSTRAINT billing_accounts_plan_code_check
  CHECK (plan_code IS NULL OR plan_code ~ '^[a-z][a-z_]{1,31}$') NOT VALID;
ALTER TABLE billing_accounts ADD CONSTRAINT billing_accounts_agency_plan_check
  CHECK (kind <> 'agency' OR plan_code IS NOT NULL) NOT VALID;

GRANT DELETE ON billing_account_stores TO app_user;

CREATE POLICY billing_lookup ON billing_accounts FOR SELECT TO app_owner USING (true);
CREATE POLICY billing_lookup ON billing_account_stores FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.agency_accounts()
  RETURNS TABLE (id uuid, name text, currency text, plan_code text, created_at timestamptz, stores bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT a.id, a.name, a.currency::text, a.plan_code, a.created_at,
         (SELECT count(*) FROM public.billing_account_stores m WHERE m.account_id = a.id)
  FROM public.billing_accounts a
  WHERE a.kind = 'agency'
  ORDER BY a.created_at DESC, a.id
  LIMIT 1000
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.agency_accounts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.agency_accounts() TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.agency_accounts();
DROP POLICY IF EXISTS billing_lookup ON billing_account_stores;
DROP POLICY IF EXISTS billing_lookup ON billing_accounts;
REVOKE DELETE ON billing_account_stores FROM app_user;
ALTER TABLE billing_accounts DROP CONSTRAINT IF EXISTS billing_accounts_agency_plan_check;
ALTER TABLE billing_accounts DROP CONSTRAINT IF EXISTS billing_accounts_plan_code_check;
ALTER TABLE billing_accounts DROP COLUMN IF EXISTS plan_code;
