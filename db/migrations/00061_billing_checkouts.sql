-- Paying an invoice on Paymob's page, which also saves the card
-- (ADR-0053).
--
-- A merchant's "pay now" opens a checkout: our reference (the Intention's
-- special_reference) and, once known, Paymob's order. Paymob's callbacks
-- reach one platform URL for every store; app.billing_checkout_store
-- places them: a transaction callback by our reference, a card token
-- callback by Paymob's order (the only link it carries). It returns the
-- store and nothing else, as the table's owner through a read-only
-- policy, like app.confirmation_store.
--
-- Expand only: a new table and a function.

-- +goose Up
CREATE TABLE billing_checkouts (
  store_id        uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  id              uuid        NOT NULL,
  account_id      uuid        NOT NULL REFERENCES billing_accounts (id),
  invoice_id      uuid        NOT NULL REFERENCES invoices (id),
  reference       text        NOT NULL CHECK (reference ~ '^bc_[0-9A-Z]{26}$'),
  paymob_order_id text        CHECK (paymob_order_id ~ '^[0-9]{1,20}$'),
  currency        char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  amount_minor    bigint      NOT NULL CHECK (amount_minor > 0),
  status          text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paid', 'failed')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (store_id, id),
  CONSTRAINT billing_checkouts_reference_key UNIQUE (reference)
);
CREATE UNIQUE INDEX billing_checkouts_paymob_order ON billing_checkouts (paymob_order_id) WHERE paymob_order_id IS NOT NULL;
CREATE INDEX billing_checkouts_account_id_idx ON billing_checkouts (account_id);
CREATE INDEX billing_checkouts_invoice_id_idx ON billing_checkouts (invoice_id);

ALTER TABLE billing_checkouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_checkouts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_checkouts
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON billing_checkouts FROM app_user;

CREATE POLICY checkout_lookup ON billing_checkouts FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.billing_checkout_store(p_reference text, p_paymob_order text)
  RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT c.store_id FROM public.billing_checkouts c
  WHERE (p_reference <> '' AND c.reference = p_reference)
     OR (p_paymob_order <> '' AND c.paymob_order_id = p_paymob_order)
  LIMIT 1
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_checkout_store(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_checkout_store(text, text) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.billing_checkout_store(text, text);
DROP TABLE IF EXISTS billing_checkouts;
