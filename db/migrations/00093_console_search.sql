-- The console's search (ADR-0079): a store owner's stores by an exact
-- email or phone, and payments by the gateway's exact reference. Both
-- cross a wall (identity's users and memberships; billing's accounts), so
-- they are SECURITY DEFINER functions that return what the console
-- shows: store IDs, or the payments' list rows.

-- +goose Up
CREATE POLICY owner_lookup ON store_memberships FOR SELECT TO app_owner USING (true);

-- app.owner_stores lists the stores (at most 20) whose owner has exactly
-- this email or one of these E.164 phones. It returns no contact.
-- +goose StatementBegin
CREATE FUNCTION app.owner_stores(p_email text, p_phones text[])
  RETURNS TABLE (store_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT m.store_id
  FROM public.users u
  JOIN public.store_memberships m ON m.user_id = u.id AND m.role = 'owner'
  WHERE (p_email <> '' AND u.email = p_email::citext) OR u.phone = ANY (p_phones)
  ORDER BY m.created_at, m.store_id
  LIMIT 20
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.owner_stores(text, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.owner_stores(text, text[]) TO app_admin;

CREATE INDEX IF NOT EXISTS payment_attempts_reference_idx ON payment_attempts (reference) WHERE reference <> '';

-- app.billing_attempts_by_reference lists the payment attempts whose
-- gateway reference is exactly p_reference, newest first, in
-- app.billing_attempts's shape.
-- +goose StatementBegin
CREATE FUNCTION app.billing_attempts_by_reference(p_reference text, p_limit int)
  RETURNS TABLE (id uuid, created_at timestamptz, invoice_id uuid, invoice_number text, account_id uuid, account_kind text,
                 account_name text, store_id uuid, gateway text, outcome text, reason text, reference text,
                 amount_minor bigint, currency text, refunded_at timestamptz)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT p.id, p.created_at, p.invoice_id, i.number, p.account_id, a.kind, a.name,
         coalesce(i.for_store_id, (SELECT m.member_store_id FROM public.billing_account_stores m
                                   WHERE m.account_id = p.account_id ORDER BY m.added_at, m.member_store_id LIMIT 1)),
         p.gateway, p.outcome, p.reason, p.reference, p.amount_minor, i.currency::text, p.refunded_at
  FROM public.payment_attempts p
  JOIN public.invoices i ON i.id = p.invoice_id
  JOIN public.billing_accounts a ON a.id = p.account_id
  WHERE p_reference <> '' AND p.reference = p_reference
  ORDER BY p.created_at DESC, p.id DESC
  LIMIT p_limit
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_attempts_by_reference(text, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_attempts_by_reference(text, int) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.billing_attempts_by_reference(text, int);
DROP INDEX IF EXISTS payment_attempts_reference_idx;
DROP FUNCTION IF EXISTS app.owner_stores(text, text[]);
DROP POLICY IF EXISTS owner_lookup ON store_memberships;
