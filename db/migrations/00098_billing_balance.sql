-- Collecting pay-as-you-sell fees (ADR-0083, approved 2026-11-04): the
-- console's collection settings, and each account's prepaid balance.
--
-- billing_settings is platform data (one row) the console writes as
-- app_admin: which ways of paying fees merchants may choose, and the
-- minimum per currency (the least top-up and the card's threshold; the
-- low-balance notice is at half of it). Both ways start off.
--
-- An account's balance is a column of billing_accounts, locked to change,
-- and every change is a balance_entries row behind the account's wall
-- (ADR-0049): append-only, each with the balance after it. collection is
-- the way the account chose; NULL until it chooses (F5).
--
-- Expand only: new tables and defaulted or nullable columns.

-- +goose Up
CREATE TABLE billing_settings (
  id                int         PRIMARY KEY CHECK (id = 1),
  prepaid_enabled   boolean     NOT NULL DEFAULT false,
  card_enabled      boolean     NOT NULL DEFAULT false,
  minimum_egp_minor bigint      NOT NULL DEFAULT 30000 CHECK (minimum_egp_minor BETWEEN 100 AND 100000000),
  minimum_sar_minor bigint      NOT NULL DEFAULT 10000 CHECK (minimum_sar_minor BETWEEN 100 AND 100000000),
  minimum_aed_minor bigint      NOT NULL DEFAULT 10000 CHECK (minimum_aed_minor BETWEEN 100 AND 100000000),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
INSERT INTO billing_settings (id) VALUES (1);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON billing_settings FROM app_user;
GRANT SELECT, UPDATE ON billing_settings TO app_admin;

ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS balance_minor bigint NOT NULL DEFAULT 0;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS collection text CHECK (collection IN ('balance', 'card')); -- migsafe:allow one row per paying account (thousands), NULL in every row

CREATE TABLE balance_entries (
  id                  uuid        PRIMARY KEY,
  account_id          uuid        NOT NULL REFERENCES billing_accounts (id),
  -- topup: paid on the gateway's page (F3); manual: added in the console;
  -- fee and fee_refund: a delivered order's fee and its return (F3).
  kind                text        NOT NULL CHECK (kind IN ('topup', 'manual', 'fee', 'fee_refund')),
  amount_minor        bigint      NOT NULL CHECK (amount_minor <> 0),
  balance_after_minor bigint      NOT NULL,
  currency            char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  -- A manual entry's reason, reference and who added it (the admin's
  -- ID, also in the admin log).
  reason              text        CHECK (reason IN ('transfer', 'compensation', 'correction')),
  reference           text        NOT NULL DEFAULT '' CHECK (char_length(reference) <= 120),
  admin_id            uuid,
  -- clock_timestamp: written under the balance's lock, so entries sort
  -- in the order they changed it (now() is the transaction's start).
  created_at          timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK ((kind = 'manual') = (reason IS NOT NULL AND admin_id IS NOT NULL)),
  CHECK (kind NOT IN ('topup', 'manual') OR amount_minor > 0)
);
-- An account's entries, newest first; leads with account_id (the FK).
CREATE INDEX balance_entries_account ON balance_entries (account_id, created_at DESC, id DESC);

ALTER TABLE balance_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE balance_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON balance_entries
  USING (account_id = app.current_account_id()) WITH CHECK (account_id = app.current_account_id());
REVOKE UPDATE, DELETE, TRUNCATE ON balance_entries FROM app_user;

-- +goose Down
DROP TABLE IF EXISTS balance_entries;
ALTER TABLE billing_accounts DROP COLUMN IF EXISTS collection;
ALTER TABLE billing_accounts DROP COLUMN IF EXISTS balance_minor;
DROP TABLE IF EXISTS billing_settings;
