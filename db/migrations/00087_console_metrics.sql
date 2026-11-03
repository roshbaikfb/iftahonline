-- The console's revenue and new stores (ADR-0075).

-- +goose Up
-- New stores by day, and the console's directory, newest first. stores
-- is small (one row per merchant), so a plain build is fine.
CREATE INDEX IF NOT EXISTS stores_created_at_idx ON stores (created_at DESC, id DESC);

-- app.billing_paying is every subscription that pays and renews, at what
-- its next renewal is charged: the plan and cycle it renews into (the
-- version on sale of its plan when its own is retired), in its account's
-- currency (its store's before its first invoice), with the store's
-- founding membership while it keeps it. RenewalDue prices the renewal
-- the same way. The console sums it into monthly revenue; it returns
-- prices only.
-- +goose StatementBegin
CREATE FUNCTION app.billing_paying()
  RETURNS TABLE (account_id uuid, kind text, currency text, plan_code text, cycle text, amount_minor bigint,
                 founding_bps int, founding_currency text, founding_plan_code text, founding_cycle text, founding_locked_minor bigint)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT coalesce(s.account_id, s.store_id), t.kind, coalesce(a.currency, st.currency)::text, t.code, coalesce(s.next_cycle, s.cycle), t.amount_minor,
         f.discount_bps, f.currency::text, f.plan_code, f.cycle, f.locked_minor
  FROM public.subscriptions s
  JOIN public.stores st ON st.id = s.store_id
  LEFT JOIN public.billing_accounts a ON a.id = s.account_id
  JOIN public.plans cur ON cur.id = coalesce(s.next_plan_id, s.plan_id)
  CROSS JOIN LATERAL (
    SELECT p.kind, p.code, x.amount_minor
    FROM public.plans p
    JOIN public.plan_prices x ON x.plan_id = p.id AND x.currency = coalesce(a.currency, st.currency) AND x.cycle = coalesce(s.next_cycle, s.cycle)
    WHERE p.id = cur.id OR (p.code = cur.code AND p.kind = cur.kind AND p.status = 'active')
    ORDER BY p.version DESC
    LIMIT 1
  ) t
  LEFT JOIN public.founding_members f ON f.store_id = s.store_id AND f.lost_at IS NULL AND t.kind = 'subscription'
  WHERE s.status = 'active' AND NOT s.cancel_at_period_end AND t.kind IN ('subscription', 'agency')
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.billing_paying() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.billing_paying() TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS app.billing_paying();
DROP INDEX IF EXISTS stores_created_at_idx;
