-- A payment that reaches an invoice already paid (ADR-0063): the owner
-- paid it on two gateway pages, or on the page while the card on file was
-- charged, or after the console settled a transfer. It is recorded as a
-- `duplicate` attempt, never dropped, and the console lists it for the
-- admin to refund by hand (the owner's decision, 2026-10-31), then marks
-- it refunded with the gateway's reference.
--
-- app_user may set only the two refund columns. The console reads every
-- account's duplicates through app.duplicate_payments, and finds one's
-- account through app.payment_attempt_account, as the table's owner
-- through a read-only policy, like app.billing_health.
--
-- Expand only: a relaxed CHECK (NOT VALID), nullable columns, indexes,
-- grants and functions.

-- +goose Up
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_outcome_check;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_outcome_check
  CHECK (outcome IN ('approved', 'declined', 'no_method', 'manual', 'duplicate')) NOT VALID;
ALTER TABLE payment_attempts ADD COLUMN IF NOT EXISTS refunded_at timestamptz;
ALTER TABLE payment_attempts ADD COLUMN IF NOT EXISTS refund_reference text;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_refund_check
  CHECK ((refunded_at IS NULL) = (refund_reference IS NULL)
     AND (refund_reference IS NULL OR char_length(refund_reference) BETWEEN 1 AND 120)
     AND (refunded_at IS NULL OR outcome = 'duplicate')) NOT VALID;
CREATE INDEX IF NOT EXISTS payment_attempts_duplicates ON payment_attempts (created_at DESC) WHERE outcome = 'duplicate';
-- A callback delivered again records its duplicate once.
CREATE UNIQUE INDEX IF NOT EXISTS payment_attempts_duplicate_once ON payment_attempts (invoice_id, gateway, reference) WHERE outcome = 'duplicate';
GRANT UPDATE (refunded_at, refund_reference) ON payment_attempts TO app_user;
CREATE POLICY billing_lookup ON payment_attempts FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE FUNCTION app.duplicate_payments()
  RETURNS TABLE (id uuid, account_id uuid, store_id uuid, invoice_number text, currency char(3), amount_minor bigint,
                 gateway text, reference text, created_at timestamptz, refunded_at timestamptz, refund_reference text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT a.id, a.account_id,
         coalesce(i.for_store_id, (SELECT m.member_store_id FROM public.billing_account_stores m
                                   WHERE m.account_id = a.account_id ORDER BY m.added_at, m.member_store_id LIMIT 1)),
         i.number, i.currency, a.amount_minor, a.gateway, a.reference, a.created_at, a.refunded_at, a.refund_reference
  FROM public.payment_attempts a
  JOIN public.invoices i ON i.id = a.invoice_id
  WHERE a.outcome = 'duplicate'
  ORDER BY a.refunded_at IS NOT NULL, a.created_at DESC
  LIMIT 200
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.duplicate_payments() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.duplicate_payments() TO app_user;

-- +goose StatementBegin
CREATE FUNCTION app.payment_attempt_account(p_id uuid)
  RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT a.account_id FROM public.payment_attempts a WHERE a.id = p_id AND a.outcome = 'duplicate'
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.payment_attempt_account(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.payment_attempt_account(uuid) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.payment_attempt_account(uuid);
DROP FUNCTION IF EXISTS app.duplicate_payments();
DROP POLICY IF EXISTS billing_lookup ON payment_attempts;
REVOKE UPDATE (refunded_at, refund_reference) ON payment_attempts FROM app_user;
DROP INDEX IF EXISTS payment_attempts_duplicate_once;
DROP INDEX IF EXISTS payment_attempts_duplicates;
ALTER TABLE payment_attempts DROP CONSTRAINT IF EXISTS payment_attempts_refund_check;
ALTER TABLE payment_attempts DROP COLUMN IF EXISTS refund_reference;
ALTER TABLE payment_attempts DROP COLUMN IF EXISTS refunded_at;
-- Duplicates already recorded are money taken: kept, so the old CHECK
-- comes back NOT VALID.
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_outcome_check;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_outcome_check
  CHECK (outcome IN ('approved', 'declined', 'no_method', 'manual')) NOT VALID;
