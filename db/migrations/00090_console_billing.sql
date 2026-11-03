-- The console's invoices and payments page: every account's invoices and
-- payment attempts, newest first, and a summary. The app role reads one
-- account at a time (RLS), so the lists are SECURITY DEFINER functions
-- in the style of app.billing_health(); an invoice's own lines are read
-- in its account's transaction. The lists are plpgsql planned per call
-- (force_custom_plan): their optional filters fold away, so a page walks
-- the (issued_at, id) index instead of sorting every invoice.

-- +goose Up
CREATE INDEX IF NOT EXISTS invoices_issued_idx ON invoices (issued_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS invoices_open_due_idx ON invoices (due_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS invoices_paid_idx ON invoices (paid_at) WHERE status = 'paid';
CREATE INDEX IF NOT EXISTS payment_attempts_created_idx ON payment_attempts (created_at DESC, id DESC);

-- app.billing_invoices lists invoices newest first (or the one p_id
-- names): p_status '' (any), open, overdue (open past due at p_now),
-- paid or void; p_currency '' or one; p_q a number prefix or a part of
-- the account's name; keyset after (p_before_at, p_before_id). Each row
-- names its account, its kind, and its store (the invoice's own, else
-- the account's first: a store account has one).
-- +goose StatementBegin
CREATE FUNCTION app.billing_invoices(p_id uuid, p_status text, p_currency text, p_q text, p_now timestamptz,
                                     p_before_at timestamptz, p_before_id uuid, p_limit int)
  RETURNS TABLE (id uuid, number text, account_id uuid, account_kind text, account_name text, store_id uuid, period date,
                 currency text, subtotal_minor bigint, tax_bps int, tax_minor bigint, total_minor bigint, status text,
                 issued_at timestamptz, due_at timestamptz, paid_at timestamptz, attempts int, last_error text,
                 next_attempt_at timestamptz)
  LANGUAGE plpgsql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
  SET plan_cache_mode = force_custom_plan
AS $$
#variable_conflict use_column
BEGIN
  RETURN QUERY
  SELECT i.id, i.number, i.account_id, a.kind, a.name,
         coalesce(i.for_store_id, (SELECT m.member_store_id FROM public.billing_account_stores m
                                   WHERE m.account_id = i.account_id ORDER BY m.added_at, m.member_store_id LIMIT 1)),
         i.period, i.currency::text, i.subtotal_minor, i.tax_bps, i.tax_minor, i.total_minor, i.status,
         i.issued_at, i.due_at, i.paid_at, i.attempts, coalesce(i.last_error, ''), i.next_attempt_at
  FROM public.invoices i
  JOIN public.billing_accounts a ON a.id = i.account_id
  WHERE (p_id IS NULL OR i.id = p_id)
    AND (p_status = '' OR (p_status = 'overdue' AND i.status = 'open' AND i.due_at < p_now)
         OR (p_status <> 'overdue' AND i.status = p_status))
    AND (p_currency = '' OR i.currency = p_currency)
    AND (p_q = '' OR i.number ILIKE p_q || '%' OR a.name ILIKE '%' || p_q || '%')
    AND (p_before_at IS NULL OR (i.issued_at, i.id) < (p_before_at, p_before_id))
  ORDER BY i.issued_at DESC, i.id DESC
  LIMIT p_limit;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_invoices(uuid, text, text, text, timestamptz, timestamptz, uuid, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_invoices(uuid, text, text, text, timestamptz, timestamptz, uuid, int) TO app_user;

-- app.billing_attempts lists payment attempts newest first: one
-- invoice's (p_invoice) or every one, by outcome and gateway ('' any).
-- +goose StatementBegin
CREATE FUNCTION app.billing_attempts(p_invoice uuid, p_outcome text, p_gateway text, p_before_at timestamptz, p_before_id uuid, p_limit int)
  RETURNS TABLE (id uuid, created_at timestamptz, invoice_id uuid, invoice_number text, account_id uuid, account_kind text,
                 account_name text, store_id uuid, gateway text, outcome text, reason text, reference text,
                 amount_minor bigint, currency text, refunded_at timestamptz)
  LANGUAGE plpgsql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
  SET plan_cache_mode = force_custom_plan
AS $$
#variable_conflict use_column
BEGIN
  RETURN QUERY
  SELECT p.id, p.created_at, p.invoice_id, i.number, p.account_id, a.kind, a.name,
         coalesce(i.for_store_id, (SELECT m.member_store_id FROM public.billing_account_stores m
                                   WHERE m.account_id = p.account_id ORDER BY m.added_at, m.member_store_id LIMIT 1)),
         p.gateway, p.outcome, p.reason, p.reference, p.amount_minor, i.currency::text, p.refunded_at
  FROM public.payment_attempts p
  JOIN public.invoices i ON i.id = p.invoice_id
  JOIN public.billing_accounts a ON a.id = p.account_id
  WHERE (p_invoice IS NULL OR p.invoice_id = p_invoice)
    AND (p_outcome = '' OR p.outcome = p_outcome)
    AND (p_gateway = '' OR p.gateway = p_gateway)
    AND (p_before_at IS NULL OR (p.created_at, p.id) < (p_before_at, p_before_id))
  ORDER BY p.created_at DESC, p.id DESC
  LIMIT p_limit;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_attempts(uuid, text, text, timestamptz, uuid, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_attempts(uuid, text, text, timestamptz, uuid, int) TO app_user;

-- app.billing_summary counts for the page's cards, one row per kind and
-- currency: open and overdue invoices (count, total), collected since
-- p_month_start (count, total with VAT, the VAT in it), and failed
-- payments since p_week_ago (count per outcome and reason; no
-- currency).
-- +goose StatementBegin
CREATE FUNCTION app.billing_summary(p_now timestamptz, p_month_start timestamptz, p_week_ago timestamptz)
  RETURNS TABLE (kind text, currency text, outcome text, reason text, n bigint, total_minor bigint, tax_minor bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT 'open', i.currency::text, '', '', count(*), sum(i.total_minor), sum(i.tax_minor)
  FROM public.invoices i WHERE i.status = 'open' GROUP BY i.currency
  UNION ALL
  SELECT 'overdue', i.currency::text, '', '', count(*), sum(i.total_minor), sum(i.tax_minor)
  FROM public.invoices i WHERE i.status = 'open' AND i.due_at < p_now GROUP BY i.currency
  UNION ALL
  SELECT 'collected', i.currency::text, '', '', count(*), sum(i.total_minor), sum(i.tax_minor)
  FROM public.invoices i WHERE i.status = 'paid' AND i.paid_at >= p_month_start GROUP BY i.currency
  UNION ALL
  SELECT 'failed', '', p.outcome, p.reason, count(*), 0::bigint, 0::bigint
  FROM public.payment_attempts p WHERE p.outcome IN ('declined', 'no_method') AND p.created_at >= p_week_ago
  GROUP BY p.outcome, p.reason
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_summary(timestamptz, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_summary(timestamptz, timestamptz, timestamptz) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.billing_summary(timestamptz, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS app.billing_attempts(uuid, text, text, timestamptz, uuid, int);
DROP FUNCTION IF EXISTS app.billing_invoices(uuid, text, text, text, timestamptz, timestamptz, uuid, int);
DROP INDEX IF EXISTS payment_attempts_created_idx;
DROP INDEX IF EXISTS invoices_paid_idx;
DROP INDEX IF EXISTS invoices_open_due_idx;
DROP INDEX IF EXISTS invoices_issued_idx;
