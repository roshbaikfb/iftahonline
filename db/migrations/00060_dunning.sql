-- Collecting invoices and dunning (ADR-0051).
--
-- An invoice is charged on its account's saved payment method; a failed
-- charge is retried 1, 3 and 5 days after the first failure. The stores
-- are past due from the first failure, suspended when the 7-day grace
-- ends unpaid, and cancelled (data due for deletion) 90 days later. A
-- payment at any point restores them.
--
-- A suspended store is held for billing: stores.billing_hold, separate
-- from the platform's own suspension (status), so lifting one never
-- lifts the other. app.resolve_host keeps its signature and reports a
-- held active store as 'held'; the storefront shows a maintenance page.
--
-- Expand only: nullable or defaulted columns, new tables, functions and
-- read-only policies for the migrating role.

-- +goose Up
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS attempts int NOT NULL DEFAULT 0;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS first_failed_at timestamptz;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS last_error text;
CREATE INDEX IF NOT EXISTS invoices_due_attempt ON invoices (next_attempt_at) WHERE status = 'open';

-- The account's card on file: the gateway's token, sealed (AAD
-- "billing-method:<account>"), and what the merchant sees of it.
CREATE TABLE billing_payment_methods (
  account_id uuid        PRIMARY KEY REFERENCES billing_accounts (id) ON DELETE CASCADE,
  gateway    text        NOT NULL CHECK (gateway ~ '^[a-z][a-z0-9_]{1,31}$'),
  token      bytea       NOT NULL,
  label      text        NOT NULL CHECK (char_length(label) BETWEEN 1 AND 60),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE payment_attempts (
  id           uuid        PRIMARY KEY,
  invoice_id   uuid        NOT NULL REFERENCES invoices (id),
  account_id   uuid        NOT NULL REFERENCES billing_accounts (id),
  gateway      text        NOT NULL,
  outcome      text        NOT NULL CHECK (outcome IN ('approved', 'declined', 'no_method', 'manual')),
  reason       text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 300),
  reference    text        NOT NULL DEFAULT '' CHECK (char_length(reference) <= 120),
  amount_minor bigint      NOT NULL CHECK (amount_minor > 0),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_attempts_invoice_id_idx ON payment_attempts (invoice_id);
CREATE INDEX payment_attempts_account_id_idx ON payment_attempts (account_id);

ALTER TABLE billing_payment_methods ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_payment_methods FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON billing_payment_methods
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());
ALTER TABLE payment_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON payment_attempts
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());
REVOKE UPDATE, TRUNCATE ON payment_attempts FROM app_user;
REVOKE DELETE ON payment_attempts FROM app_user;
REVOKE TRUNCATE ON billing_payment_methods FROM app_user;

ALTER TABLE stores ADD COLUMN IF NOT EXISTS billing_hold boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.resolve_host(p_host text)
  RETURNS TABLE (store_id uuid, store_status text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT d.store_id, CASE WHEN s.status = 'active' AND s.billing_hold THEN 'held' ELSE s.status END
  FROM public.store_domains d
  JOIN public.stores s ON s.id = d.store_id
  WHERE d.host = p_host::public.citext AND d.status = 'verified'
$$;
-- +goose StatementEnd

-- The hourly run's work, as the tables' owner (read-only policies):
-- invoices to charge, stores to suspend and to cancel. IDs only.
CREATE POLICY billing_lookup ON invoices FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.dunning_due(p_now timestamptz)
  RETURNS TABLE (task text, id uuid, account_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  (SELECT 'charge', i.id, i.account_id FROM public.invoices i
   WHERE i.status = 'open' AND i.next_attempt_at IS NOT NULL AND i.next_attempt_at <= p_now
   ORDER BY i.next_attempt_at LIMIT 5000)
  UNION ALL
  (SELECT 'suspend', s.store_id, s.account_id FROM public.subscriptions s
   WHERE s.status = 'past_due' AND s.grace_until <= p_now LIMIT 5000)
  UNION ALL
  (SELECT 'cancel', s.store_id, s.account_id FROM public.subscriptions s
   WHERE s.status = 'suspended' AND s.suspended_at <= p_now - interval '90 days' LIMIT 5000)
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.dunning_due(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.dunning_due(timestamptz) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.dunning_due(timestamptz);
DROP POLICY IF EXISTS billing_lookup ON invoices;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.resolve_host(p_host text)
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
ALTER TABLE stores DROP COLUMN IF EXISTS billing_hold;
DROP TABLE IF EXISTS payment_attempts;
DROP TABLE IF EXISTS billing_payment_methods;
DROP INDEX IF EXISTS invoices_due_attempt;
ALTER TABLE invoices DROP COLUMN IF EXISTS last_error;
ALTER TABLE invoices DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE invoices DROP COLUMN IF EXISTS first_failed_at;
ALTER TABLE invoices DROP COLUMN IF EXISTS attempts;
