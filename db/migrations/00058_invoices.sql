-- Billing accounts and monthly invoices (ADR-0049).
--
-- An account is who pays: one store, or an agency with many. Invoices
-- belong to the account, not to a store, so an agency gets one invoice
-- for all its stores. Account-owned tables have no store_id; they sit
-- behind the same second wall as tenant tables, keyed on the account:
-- app.account_id, set per transaction (SET LOCAL) by the billing module,
-- from the store's own subscription or a system job.
--
-- A transaction may set both app.store_id and app.account_id: an invoice
-- is written in the account's wall and its fees marked in each store's,
-- atomically.
--
-- invoice_counters gives gapless numbers per year (INV-2026-000001): the
-- counter's row is updated in the invoice's own transaction.
--
-- Expand only: new tables, nullable columns, NOT VALID foreign keys and
-- read-only policies for the migrating role.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION app.current_account_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
AS $$
  SELECT nullif(current_setting('app.account_id', true), '')::uuid
$$;
-- +goose StatementEnd

CREATE TABLE billing_accounts (
  id         uuid        PRIMARY KEY,
  kind       text        NOT NULL CHECK (kind IN ('store', 'agency')),
  name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  currency   char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  country    char(2)     NOT NULL CHECK (country IN ('EG', 'SA', 'AE')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- The stores an account pays for: one for a store's own account, many
-- for an agency's. A store is in one account.
CREATE TABLE billing_account_stores (
  account_id      uuid        NOT NULL REFERENCES billing_accounts (id) ON DELETE CASCADE,
  member_store_id uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  added_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, member_store_id),
  CONSTRAINT billing_account_stores_one_account UNIQUE (member_store_id)
);

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS account_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES billing_accounts (id) NOT VALID;
CREATE INDEX IF NOT EXISTS subscriptions_account_id_idx ON subscriptions (account_id);

-- VAT by the account's country (approved 2026-10-30); e-invoicing (ETA,
-- ZATCA) is not part of P7.
CREATE TABLE tax_rates (
  country  char(2) PRIMARY KEY CHECK (country IN ('EG', 'SA', 'AE')),
  rate_bps int     NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  name_ar  text    NOT NULL,
  name_en  text    NOT NULL
);
INSERT INTO tax_rates (country, rate_bps, name_ar, name_en) VALUES
  ('EG', 1400, 'ضريبة القيمة المضافة', 'VAT'),
  ('SA', 1500, 'ضريبة القيمة المضافة', 'VAT'),
  ('AE', 500, 'ضريبة القيمة المضافة', 'VAT');

CREATE TABLE invoice_counters (
  year int    PRIMARY KEY CHECK (year BETWEEN 2026 AND 9999),
  last bigint NOT NULL DEFAULT 0 CHECK (last >= 0)
);

CREATE TABLE invoices (
  id             uuid        PRIMARY KEY,
  account_id     uuid        NOT NULL REFERENCES billing_accounts (id),
  number         text        NOT NULL CHECK (number ~ '^INV-[0-9]{4}-[0-9]{6,}$'),
  period         date        NOT NULL CHECK (extract(day FROM period) = 1),
  currency       char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  subtotal_minor bigint      NOT NULL CHECK (subtotal_minor > 0),
  tax_bps        int         NOT NULL CHECK (tax_bps BETWEEN 0 AND 10000),
  tax_minor      bigint      NOT NULL CHECK (tax_minor >= 0),
  total_minor    bigint      NOT NULL CHECK (total_minor = subtotal_minor + tax_minor),
  status         text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paid', 'void')),
  issued_at      timestamptz NOT NULL,
  due_at         timestamptz NOT NULL,
  paid_at        timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT invoices_number_key UNIQUE (number),
  -- One invoice per account and month; leads with account_id (the FK).
  CONSTRAINT invoices_account_period_key UNIQUE (account_id, period),
  CHECK ((status = 'paid') = (paid_at IS NOT NULL))
);

CREATE TABLE invoice_lines (
  invoice_id    uuid   NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
  account_id    uuid   NOT NULL REFERENCES billing_accounts (id),
  line_no       int    NOT NULL CHECK (line_no >= 1),
  kind          text   NOT NULL CHECK (kind IN ('subscription', 'sell_fees', 'fee_credit')),
  line_store_id uuid   REFERENCES stores (id) ON DELETE SET NULL,
  description   text   NOT NULL CHECK (char_length(description) BETWEEN 1 AND 300),
  quantity      bigint NOT NULL CHECK (quantity >= 1),
  amount_minor  bigint NOT NULL,
  PRIMARY KEY (invoice_id, line_no),
  CHECK ((kind = 'fee_credit') = (amount_minor < 0))
);
CREATE INDEX invoice_lines_account_id_idx ON invoice_lines (account_id);
CREATE INDEX invoice_lines_line_store_id_idx ON invoice_lines (line_store_id);

-- A renewal billed once: set when an invoice bills the subscription's
-- next period, cleared when that period is paid (7d).
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS renewal_invoice_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_renewal_invoice_id_fkey
  FOREIGN KEY (renewal_invoice_id) REFERENCES invoices (id) NOT VALID;
CREATE INDEX IF NOT EXISTS subscriptions_renewal_invoice_id_idx ON subscriptions (renewal_invoice_id);

ALTER TABLE sell_fees ADD COLUMN IF NOT EXISTS invoice_id uuid;
ALTER TABLE sell_fees ADD CONSTRAINT sell_fees_invoice_id_fkey FOREIGN KEY (invoice_id) REFERENCES invoices (id) NOT VALID;
CREATE INDEX IF NOT EXISTS sell_fees_invoice_id_idx ON sell_fees (invoice_id);

-- The account wall.
ALTER TABLE billing_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON billing_accounts
  USING (id = app.current_account_id()) WITH CHECK (id = app.current_account_id());
ALTER TABLE billing_account_stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_account_stores FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON billing_account_stores
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON invoices
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());
ALTER TABLE invoice_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON invoice_lines
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());

REVOKE DELETE, TRUNCATE ON billing_accounts, billing_account_stores, invoices, invoice_lines FROM app_user;
REVOKE UPDATE ON invoice_lines FROM app_user;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON tax_rates FROM app_user;
REVOKE DELETE, TRUNCATE ON invoice_counters FROM app_user;

-- The monthly run's list of stores with something to bill, as the
-- tables' owner (FORCE RLS binds it too: read-only policies let it see
-- every store's rows; app_user only gets the function, which returns
-- store IDs and nothing else).
CREATE POLICY billing_lookup ON subscriptions FOR SELECT TO app_owner USING (true);
CREATE POLICY billing_lookup ON sell_fees FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.billable_stores(p_period date)
  RETURNS SETOF uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT s.store_id FROM public.subscriptions s JOIN public.plans p ON p.id = s.plan_id
  WHERE p.kind IN ('subscription', 'agency') AND s.status = 'active' AND NOT s.cancel_at_period_end
    AND s.renewal_invoice_id IS NULL AND s.period_end IS NOT NULL AND s.period_end <= (p_period + interval '1 month')
  UNION
  SELECT f.store_id FROM public.sell_fees f
  WHERE (f.period <= p_period AND f.status = 'due') OR f.status = 'credit_due'
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billable_stores(date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billable_stores(date) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.billable_stores(date);
DROP POLICY IF EXISTS billing_lookup ON sell_fees;
DROP POLICY IF EXISTS billing_lookup ON subscriptions;
DROP INDEX IF EXISTS sell_fees_invoice_id_idx;
ALTER TABLE sell_fees DROP CONSTRAINT IF EXISTS sell_fees_invoice_id_fkey;
ALTER TABLE sell_fees DROP COLUMN IF EXISTS invoice_id;
DROP INDEX IF EXISTS subscriptions_renewal_invoice_id_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_renewal_invoice_id_fkey;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS renewal_invoice_id;
DROP TABLE IF EXISTS invoice_lines;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS invoice_counters;
DROP TABLE IF EXISTS tax_rates;
DROP INDEX IF EXISTS subscriptions_account_id_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_account_id_fkey;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS account_id;
DROP TABLE IF EXISTS billing_account_stores;
DROP TABLE IF EXISTS billing_accounts;
DROP FUNCTION IF EXISTS app.current_account_id();
