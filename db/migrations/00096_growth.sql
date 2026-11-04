-- The console's growth page (ADR-0081): what billing can tell about the
-- past (revenue collected, stores that paid), and the console's own daily
-- record of what it cannot (the recurring revenue and who pays).

-- +goose Up

-- app.billing_collected is the revenue collected per month in p_tz since
-- p_since: paid invoices, before VAT, per currency.
-- +goose StatementBegin
CREATE FUNCTION app.billing_collected(p_since timestamptz, p_tz text)
  RETURNS TABLE (month date, currency text, invoices bigint, subtotal_minor bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT date_trunc('month', i.paid_at AT TIME ZONE p_tz)::date, i.currency::text, count(*), sum(i.subtotal_minor)::bigint
  FROM public.invoices i
  WHERE i.status = 'paid' AND i.paid_at >= p_since
  GROUP BY 1, 2
  ORDER BY 1, 2
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_collected(timestamptz, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_collected(timestamptz, text) TO app_user;

-- The owner reads every account's lines, as it reads every invoice
-- (00060): FORCE RLS binds it too. app_user only gets the function below,
-- which returns store IDs and nothing else.
CREATE POLICY billing_lookup ON invoice_lines FOR SELECT TO app_owner USING (true);

-- app.billing_converted are the stores that paid a subscription since
-- p_since: a paid invoice with a subscription line for the store.
-- +goose StatementBegin
CREATE FUNCTION app.billing_converted(p_since timestamptz)
  RETURNS TABLE (store_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT DISTINCT l.line_store_id
  FROM public.invoices i
  JOIN public.invoice_lines l ON l.invoice_id = i.id
  WHERE i.status = 'paid' AND i.paid_at >= p_since
    AND l.kind = 'subscription' AND l.line_store_id IS NOT NULL
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_converted(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_converted(timestamptz) TO app_user;

-- The recurring revenue per Cairo day and currency, the day's latest
-- reading.
CREATE TABLE admin_growth_mrr (
  day       date        NOT NULL,
  currency  char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  mrr_minor bigint      NOT NULL CHECK (mrr_minor >= 0),
  accounts  bigint      NOT NULL CHECK (accounts >= 0),
  taken_at  timestamptz NOT NULL,
  PRIMARY KEY (day, currency)
);

-- The paying accounts as a Cairo month began: churn compares one month's
-- set with the next. taken_on says when the set was read; only a set read
-- in the month's first days counts as its start.
CREATE TABLE admin_growth_months (
  month    date NOT NULL PRIMARY KEY CHECK (extract(day FROM month) = 1),
  taken_on date NOT NULL CHECK (taken_on >= month)
);
CREATE TABLE admin_growth_paying (
  month      date NOT NULL REFERENCES admin_growth_months (month) ON DELETE CASCADE,
  account_id uuid NOT NULL,
  PRIMARY KEY (month, account_id)
);

REVOKE ALL ON admin_growth_mrr, admin_growth_months, admin_growth_paying FROM app_user;
GRANT SELECT, INSERT, UPDATE ON admin_growth_mrr TO app_admin;
GRANT SELECT, INSERT ON admin_growth_months, admin_growth_paying TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS admin_growth_paying;
DROP TABLE IF EXISTS admin_growth_months;
DROP TABLE IF EXISTS admin_growth_mrr;
DROP FUNCTION IF EXISTS app.billing_converted(timestamptz);
DROP POLICY IF EXISTS billing_lookup ON invoice_lines;
DROP FUNCTION IF EXISTS app.billing_collected(timestamptz, text);
