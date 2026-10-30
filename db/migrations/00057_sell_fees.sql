-- Pay-as-you-sell fees (ADR-0048): a fee on each order the store
-- delivered, while its plan charges one, invoiced monthly (7c-2).
--
-- plan_fees is platform data like the rest of a plan (00056): the rate
-- and its bounds per currency. A plan without a row charges no fee.
--
-- sell_fees is a tenant table: one row per delivered order, written when
-- the order is delivered, with the rate and plan of that moment.
--   due        to be invoiced
--   cancelled  returned before it was invoiced
--   invoiced   on an invoice (7c-2)
--   credit_due returned after it was invoiced: credited on the next one
--   credited   credited (7c-2)
-- order_id has no FK: orders are partitioned by month (ADR-0021).
--
-- The seeded rate is a placeholder, to be set before launch.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE plan_fees (
  plan_id   uuid    NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
  currency  char(3) NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  rate_bps  int     NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  min_minor bigint  NOT NULL DEFAULT 0 CHECK (min_minor >= 0),
  max_minor bigint  CHECK (max_minor IS NULL OR max_minor >= min_minor),
  -- Leads with plan_id, so it also covers the FK.
  PRIMARY KEY (plan_id, currency)
);

INSERT INTO plan_fees (plan_id, currency, rate_bps)
SELECT p.id, c.currency, 250
FROM plans p CROSS JOIN (VALUES ('EGP'), ('SAR'), ('AED')) AS c (currency)
WHERE p.kind = 'pays' AND p.status = 'active';

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON plan_fees FROM app_user;

CREATE TABLE sell_fees (
  store_id     uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  order_id     uuid        NOT NULL,
  period       date        NOT NULL CHECK (extract(day FROM period) = 1),
  plan_id      uuid        NOT NULL REFERENCES plans (id),
  base_minor   bigint      NOT NULL CHECK (base_minor >= 0),
  rate_bps     int         NOT NULL CHECK (rate_bps BETWEEN 0 AND 10000),
  fee_minor    bigint      NOT NULL CHECK (fee_minor >= 0 AND fee_minor <= base_minor),
  currency     char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  status       text        NOT NULL DEFAULT 'due' CHECK (status IN ('due', 'cancelled', 'invoiced', 'credit_due', 'credited')),
  delivered_at timestamptz NOT NULL,
  returned_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  -- Leads with store_id, so it also covers the stores FK.
  PRIMARY KEY (store_id, order_id),
  CHECK ((status IN ('cancelled', 'credit_due', 'credited')) = (returned_at IS NOT NULL))
);
-- What a month's invoice reads.
CREATE INDEX sell_fees_store_period ON sell_fees (store_id, period, status);
CREATE INDEX sell_fees_plan_id_idx ON sell_fees (plan_id);

ALTER TABLE sell_fees ENABLE ROW LEVEL SECURITY;
ALTER TABLE sell_fees FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sell_fees
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
REVOKE DELETE, TRUNCATE ON sell_fees FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS sell_fees;
DROP TABLE IF EXISTS plan_fees;
