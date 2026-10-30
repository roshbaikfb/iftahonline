-- The webhook's lookup of many codes at once (P6 review): a batch of
-- customers' texts (Meta sends up to 1,000 updates in one) finds the
-- stores of all its codes in one query, not one per text. As
-- app.confirmation_store (00048): runs as the table's owner, returns only
-- each code's store, and app_user may only call it.
--
-- Expand only: a new function; app.confirmation_store stays until no
-- running binary calls it.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.confirmation_stores(p_codes text[])
  RETURNS TABLE (code text, store_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT c.wa_code, c.store_id FROM public.order_confirmations c
  WHERE c.wa_code = ANY (p_codes[1:1000])
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.confirmation_stores(text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.confirmation_stores(text[]) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.confirmation_stores(text[]);
