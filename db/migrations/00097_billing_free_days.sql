-- A store whose grace ran out unpaid is an ordinary free store for 30
-- days before its storefront closes (ADR-0082, amending ADR-0051 §4).
--
-- subscriptions.held_at is when billing held the store. The hourly
-- dunning gains a 'hold' task: suspended stores, not held yet, suspended
-- 30 days ago (one already due for cancelling is held by its cancel).
-- Stores suspended before this migration were held at once, so they are
-- held from their suspension.
--
-- Expand only: a nullable column, a partial index, a function replaced
-- with the same signature.

-- +goose Up
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS held_at timestamptz;
CREATE INDEX IF NOT EXISTS subscriptions_to_hold ON subscriptions (suspended_at)
  WHERE status = 'suspended' AND held_at IS NULL;

-- FORCE RLS binds the migrating role too: it reads every subscription
-- (00058's billing_lookup) and updates them only for this backfill.
CREATE POLICY billing_backfill ON subscriptions FOR UPDATE TO app_owner USING (true) WITH CHECK (true);
UPDATE subscriptions SET held_at = suspended_at
WHERE status IN ('suspended', 'cancelled') AND held_at IS NULL AND suspended_at IS NOT NULL;
DROP POLICY billing_backfill ON subscriptions;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.dunning_due(p_now timestamptz)
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
  (SELECT 'hold', s.store_id, s.account_id FROM public.subscriptions s
   WHERE s.status = 'suspended' AND s.held_at IS NULL AND s.suspended_at <= p_now - interval '30 days'
     AND s.suspended_at > p_now - interval '90 days' LIMIT 5000)
  UNION ALL
  (SELECT 'cancel', s.store_id, s.account_id FROM public.subscriptions s
   WHERE s.status = 'suspended' AND s.suspended_at <= p_now - interval '90 days' LIMIT 5000)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.dunning_due(p_now timestamptz)
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
DROP INDEX IF EXISTS subscriptions_to_hold;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS held_at;
