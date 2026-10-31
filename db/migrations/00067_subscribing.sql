-- Merchants choose their plan (ADR-0060).
--
-- Subscribing from the free plan, a trial or pay as you sell is paid at
-- once on the gateway's page (approved 2026-10-31): an invoice of
-- purpose 'subscribe' for the first period of plan_id and cycle, for
-- one store. It is never charged by dunning (no next attempt), and
-- paying it starts the period then. The monthly invoice keeps its one
-- per account and month, now as a partial unique index: a subscribe
-- invoice may share the month.
--
-- A change between paid plans, or of the cycle, applies at the next
-- renewal (approved: no proration): subscriptions.next_plan_id and
-- next_cycle. A paid plan left for pay as you sell does so at the end
-- of the paid period, as a cancellation to next_plan_id.
--
-- Backwards compatible: new nullable columns and defaults, NOT VALID
-- checks and keys; the dropped constraint is replaced by a narrower
-- index first, so running code keeps its guarantee.

-- +goose Up
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'monthly';
ALTER TABLE invoices ADD CONSTRAINT invoices_purpose_check CHECK (purpose IN ('monthly', 'subscribe')) NOT VALID;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS plan_id uuid;
ALTER TABLE invoices ADD CONSTRAINT invoices_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES plans (id) NOT VALID;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS cycle text;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS for_store_id uuid;
ALTER TABLE invoices ADD CONSTRAINT invoices_for_store_id_fkey FOREIGN KEY (for_store_id) REFERENCES stores (id) NOT VALID;
ALTER TABLE invoices ADD CONSTRAINT invoices_subscribe_check
  CHECK (purpose <> 'subscribe' OR (plan_id IS NOT NULL AND cycle IN ('monthly', 'annual') AND for_store_id IS NOT NULL)) NOT VALID;
CREATE INDEX IF NOT EXISTS invoices_plan_id_idx ON invoices (plan_id) WHERE plan_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS invoices_for_store_id_idx ON invoices (for_store_id) WHERE for_store_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS invoices_monthly_key ON invoices (account_id, period) WHERE purpose = 'monthly';
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_account_period_key;
-- The account's invoices, by the FK, still indexed after the drop.
CREATE INDEX IF NOT EXISTS invoices_account_id_idx ON invoices (account_id, issued_at DESC);

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS next_plan_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_next_plan_id_fkey FOREIGN KEY (next_plan_id) REFERENCES plans (id) NOT VALID;
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS next_cycle text;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_next_cycle_check CHECK (next_cycle IS NULL OR next_cycle IN ('monthly', 'annual')) NOT VALID;
CREATE INDEX IF NOT EXISTS subscriptions_next_plan_id_idx ON subscriptions (next_plan_id) WHERE next_plan_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS subscriptions_next_plan_id_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_next_cycle_check;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_next_plan_id_fkey;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS next_cycle;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS next_plan_id;
DROP INDEX IF EXISTS invoices_account_id_idx;
ALTER TABLE invoices ADD CONSTRAINT invoices_account_period_key UNIQUE (account_id, period);
DROP INDEX IF EXISTS invoices_monthly_key;
DROP INDEX IF EXISTS invoices_for_store_id_idx;
DROP INDEX IF EXISTS invoices_plan_id_idx;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_subscribe_check;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_for_store_id_fkey;
ALTER TABLE invoices DROP COLUMN IF EXISTS for_store_id;
ALTER TABLE invoices DROP COLUMN IF EXISTS cycle;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_plan_id_fkey;
ALTER TABLE invoices DROP COLUMN IF EXISTS plan_id;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_purpose_check;
ALTER TABLE invoices DROP COLUMN IF EXISTS purpose;
