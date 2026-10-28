-- Customer-started confirmation on WhatsApp (ADR-0041). The thank-you
-- page offers a "confirm on WhatsApp" link to the platform's number,
-- with a ready text carrying the order's short code. That text reaches
-- the platform's one number for every store, so the code alone must name
-- its store: it is unique across stores, and app.confirmation_store
-- maps it to the store (and nothing else) for the webhook.
--
-- Expand only: a nullable column, a NOT VALID check, a partial index
-- and a function.

-- +goose Up
ALTER TABLE order_confirmations ADD COLUMN IF NOT EXISTS wa_code text;
ALTER TABLE order_confirmations ADD CONSTRAINT order_confirmations_wa_code_check
  CHECK (wa_code ~ '^[0-9A-HJKMNP-TV-Z]{8}$') NOT VALID;
CREATE UNIQUE INDEX IF NOT EXISTS order_confirmations_wa_code ON order_confirmations (wa_code) WHERE wa_code IS NOT NULL;

-- The webhook's lookup: the store of a code, as the table's owner
-- (FORCE RLS binds the owner too, so a read-only policy lets it see
-- every store's codes; app_user only gets this function).
DROP POLICY IF EXISTS code_lookup ON order_confirmations;
CREATE POLICY code_lookup ON order_confirmations FOR SELECT TO app_owner USING (true);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.confirmation_store(p_code text)
  RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT c.store_id FROM public.order_confirmations c WHERE c.wa_code = p_code
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.confirmation_store(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.confirmation_store(text) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.confirmation_store(text);
DROP POLICY IF EXISTS code_lookup ON order_confirmations;
DROP INDEX IF EXISTS order_confirmations_wa_code;
ALTER TABLE order_confirmations DROP CONSTRAINT IF EXISTS order_confirmations_wa_code_check;
ALTER TABLE order_confirmations DROP COLUMN IF EXISTS wa_code;
